package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/service"
)

// services tracks the set of declarative service files the daemon has
// loaded from ~/.godo/services/. Keyed by the file's absolute path.
type services struct {
	mu    sync.Mutex
	specs map[string]*service.Spec
}

func newServices() *services {
	return &services{specs: make(map[string]*service.Spec)}
}

func (s *services) snapshot() []*service.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*service.Spec, 0, len(s.specs))
	for _, spec := range s.specs {
		out = append(out, spec)
	}
	return out
}

func (s *services) put(spec *service.Spec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs[spec.Path] = spec
}

func (s *services) remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.specs, path)
}

// loadServicesOnBoot scans the services dir, loads everything it can,
// and returns the slice. Per-file errors are warned but don't stop boot.
func (d *Daemon) loadServicesOnBoot() []*service.Spec {
	specs, err := service.LoadAll(d.serviceDir)
	if err != nil {
		slog.Warn("service load errors at boot", "err", err)
	}
	return specs
}

// reconcileBootedJobs marks any Running-state job from the persisted
// registry as Failed: their PIDs are stale because the daemon just
// restarted. Service-tagged jobs are about to be replaced by autostart
// anyway; ad-hoc jobs are gone for good.
func (d *Daemon) reconcileBootedJobs() {
	now := time.Now()
	for _, snap := range d.registry.Snapshot() {
		if snap.State != job.Running {
			continue
		}
		_ = d.registry.Update(snap.Hash, func(j *job.Job) {
			if j.State == job.Running {
				j.State = job.Failed
				j.ExitedAt = now
			}
		})
	}
}

// applyServicesOnBoot loads every spec on disk and autostarts those
// flagged. Idempotent: a job for the same ServiceFile is reused so
// service identity stays stable across daemon restarts.
func (d *Daemon) applyServicesOnBoot() {
	specs := d.loadServicesOnBoot()
	for _, s := range specs {
		d.svc.put(s)
		if !s.Autostart {
			continue
		}
		if err := d.startServiceJob(s); err != nil {
			slog.Warn("service autostart failed", "service", s.Name, "err", err)
		}
	}
}

// startServiceJob runs the spec — reusing an existing service-tagged
// job entry if one exists for this Path, otherwise creating fresh.
func (d *Daemon) startServiceJob(spec *service.Spec) error {
	hash, found := d.findServiceJobHash(spec.Path)
	var jobToStart *job.Job
	if found {
		// Reset and update fields from the (possibly edited) spec.
		_ = d.registry.Update(hash, func(j *job.Job) {
			j.State = job.Pending
			j.PID = 0
			j.ExitCode = 0
			j.ExitedAt = time.Time{}
			j.Name = spec.Name
			j.Command = spec.Command
			j.Args = spec.Args
			j.WorkingDir = spec.WorkingDir
			j.Env = spec.Env
			j.Nice = spec.Nice
			j.IOnice = spec.IOnice
			if spec.Restart != "" {
				j.Restart = job.RestartPolicy(spec.Restart)
			}
			j.CronSchedule = spec.Cron.Schedule
		})
		snap, err := d.registry.GetCopy(hash)
		if err != nil {
			return fmt.Errorf("get reused job: %w", err)
		}
		jobToStart = &snap
	} else {
		j := job.New(spec.Command, spec.Args, spec.JobOptions()...)
		if err := d.registry.Add(j); err != nil {
			return fmt.Errorf("register: %w", err)
		}
		jobToStart = j
	}
	if err := d.runner.Start(jobToStart); err != nil {
		if !found {
			d.registry.Remove(jobToStart.Hash)
		}
		return fmt.Errorf("start: %w", err)
	}
	return nil
}

func (d *Daemon) findServiceJobHash(path string) (string, bool) {
	for _, snap := range d.registry.Snapshot() {
		if snap.ServiceFile == path {
			return snap.Hash, true
		}
	}
	return "", false
}

func (d *Daemon) stopServiceJob(path string) {
	hash, found := d.findServiceJobHash(path)
	if !found {
		return
	}
	_ = d.runner.Stop(hash)
}

// reloadServices rescans the on-disk services dir, diffs against the
// in-memory snapshot, and applies the changes.
//
// Added   -> install + autostart
// Removed -> stop the job (entry stays in registry as Cancelled for history)
// Modified-> update in-memory spec only; user must restart explicitly
//
//	(matches systemd's `daemon-reload` semantics)
func (d *Daemon) reloadServices() (service.Diff, []error) {
	current := d.svc.snapshot()
	next, loadErr := service.LoadAll(d.serviceDir)
	var errs []error
	if loadErr != nil {
		errs = append(errs, loadErr)
	}
	diff := service.DiffSpecs(current, next)

	for _, spec := range diff.Added {
		d.svc.put(spec)
		if spec.Autostart {
			if err := d.startServiceJob(spec); err != nil {
				errs = append(errs, fmt.Errorf("start %s: %w", spec.Name, err))
			}
		}
	}
	for _, path := range diff.Removed {
		d.stopServiceJob(path)
		d.svc.remove(path)
	}
	for _, spec := range diff.Modified {
		d.svc.put(spec)
		// Intentionally don't auto-restart — user runs `godo restart` if
		// they want the new spec to take effect.
	}
	return diff, errs
}

// importServiceFile validates srcPath, copies it into the services
// dir, and installs it. Returns the resulting Spec.
func (d *Daemon) importServiceFile(srcPath string) (*service.Spec, error) {
	abs, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, fmt.Errorf("absolute path: %w", err)
	}
	// Validate first so we never copy a broken file in.
	probe, err := service.Load(abs)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(d.serviceDir, 0o755); err != nil {
		return nil, fmt.Errorf("create services dir: %w", err)
	}

	// Refuse to install a second spec with the same Name even if its
	// source is a different file — the daemon's services map and the
	// registry both treat Name as identity.
	for _, existing := range d.svc.snapshot() {
		if existing.Name == probe.Name {
			return nil, fmt.Errorf("a service named %q is already loaded (from %s)", probe.Name, existing.Path)
		}
	}

	// Destination filename comes from the spec's Name so source filenames
	// from temp dirs / version control / ad-hoc paths normalize cleanly.
	dst := filepath.Join(d.serviceDir, probe.Name+".toml")
	if abs != dst {
		if _, err := os.Stat(dst); err == nil {
			return nil, fmt.Errorf("a service file already exists at %s; remove or rename first", dst)
		}
		if err := copyFile(abs, dst); err != nil {
			return nil, fmt.Errorf("copy: %w", err)
		}
	}

	loaded, err := service.Load(dst)
	if err != nil {
		// Roll back the copy if validation somehow flips between probes.
		if abs != dst {
			_ = os.Remove(dst)
		}
		return nil, err
	}
	d.svc.put(loaded)
	if loaded.Autostart {
		if err := d.startServiceJob(loaded); err != nil {
			return loaded, fmt.Errorf("autostart: %w", err)
		}
	}
	_ = probe // silence unused
	return loaded, nil
}

func copyFile(src, dst string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o644)
}
