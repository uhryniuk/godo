package daemon

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	robfig "github.com/robfig/cron/v3"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/service"
)

// cronParser accepts the standard 5-field cron schedule plus the
// @descriptor forms (@hourly, @daily, @every 5m, …). Used by both the
// runtime scheduler and Spec.Validate so a spec that survives load is
// guaranteed to be schedulable.
var cronParser = robfig.NewParser(
	robfig.SecondOptional | robfig.Minute | robfig.Hour |
		robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor,
)

// cronner wraps robfig/cron with the daemon's per-spec bookkeeping.
// Add/Remove are keyed by spec.Path so reloads can re-register without
// duplicating entries.
type cronner struct {
	cron  *robfig.Cron
	spawn func(*service.Spec) // injected: how to start a tick

	mu      sync.Mutex
	entries map[string]robfig.EntryID
}

func newCronner(spawn func(*service.Spec)) *cronner {
	c := &cronner{
		cron:    robfig.New(robfig.WithParser(cronParser)),
		spawn:   spawn,
		entries: make(map[string]robfig.EntryID),
	}
	return c
}

// Start kicks the underlying cron loop. Safe to call once.
func (c *cronner) Start() { c.cron.Start() }

// Stop blocks until any in-flight tick callback returns. Safe to call
// from daemon shutdown — guaranteed no new ticks fire after this.
func (c *cronner) Stop() {
	ctx := c.cron.Stop()
	<-ctx.Done()
}

// Register hooks spec into the scheduler. If a prior entry exists for
// spec.Path it is removed first (idempotent for reload-modified case).
// No-op for specs without a Cron.Schedule.
func (c *cronner) Register(spec *service.Spec) error {
	if spec.Cron.Schedule == "" {
		return nil
	}
	c.mu.Lock()
	if old, ok := c.entries[spec.Path]; ok {
		c.cron.Remove(old)
		delete(c.entries, spec.Path)
	}
	c.mu.Unlock()

	captured := spec
	id, err := c.cron.AddFunc(spec.Cron.Schedule, func() {
		c.spawn(captured)
	})
	if err != nil {
		return fmt.Errorf("cron schedule %q: %w", spec.Cron.Schedule, err)
	}
	c.mu.Lock()
	c.entries[spec.Path] = id
	c.mu.Unlock()
	return nil
}

// Unregister drops the entry for spec.Path. No-op if absent.
func (c *cronner) Unregister(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.entries[path]; ok {
		c.cron.Remove(id)
		delete(c.entries, path)
	}
}

// spawnCronJob is the daemon's tick callback: registers a fresh Job
// instance per fire and starts it. Honors Spec.Cron.Overlap — when
// false (default), a tick is suppressed if a prior Job from this spec
// is still Running.
//
// Cron-fired jobs always have Restart=No; cron itself is the repeat
// mechanism. Their Name is "<spec.Name>@<unix-second>" so concurrent
// runs (when Overlap=true) don't collide on the registry's name index.
func (d *Daemon) spawnCronJob(spec *service.Spec) {
	if !spec.Cron.Overlap {
		for _, snap := range d.registry.Snapshot() {
			if snap.ServiceFile == spec.Path && snap.State == job.Running {
				slog.Info("cron skipped: previous run still running",
					"service", spec.Name)
				return
			}
		}
	}

	name := fmt.Sprintf("%s@%d", spec.Name, time.Now().Unix())
	opts := append(spec.JobOptions(), job.WithName(name), job.WithRestart(job.RestartNo))
	j := job.New(spec.Command, spec.Args, opts...)
	if err := d.registry.Add(j); err != nil {
		slog.Warn("cron register failed", "service", spec.Name, "err", err)
		return
	}
	if err := d.runner.Start(j); err != nil {
		d.registry.Remove(j.Hash)
		slog.Warn("cron start failed", "service", spec.Name, "err", err)
	}
}
