package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/uhryniuk/godo/internal/job"
)

// Runner spawns and supervises child processes. One Runner per daemon.
type Runner struct {
	registry *Registry
	saveFn   func() error // called after every state transition
	onExit   func(hash string, exitCode int, state job.State)

	mu       sync.Mutex
	procs    map[string]*exec.Cmd // hash -> cmd, used by Stop and the watcher
	watchers sync.WaitGroup       // tracks live watch goroutines
}

// NewRunner constructs a Runner. saveFn is invoked after every job state
// transition; pass a no-op if you don't need persistence.
func NewRunner(reg *Registry, saveFn func() error) *Runner {
	return &Runner{
		registry: reg,
		saveFn:   saveFn,
		procs:    make(map[string]*exec.Cmd),
	}
}

// Start spawns j's process. Returns once the child is started (PID known)
// or with an error if exec failed. A goroutine watches for exit and
// updates j's terminal state via the registry's locked Update path so
// concurrent reads (e.g. List) stay race-free.
//
// j must already be registered in r.registry before calling Start.
func (r *Runner) Start(j *job.Job) error {
	if !j.State.CanTransition(job.Running) {
		return fmt.Errorf("cannot start job in state %s", j.State)
	}

	if err := os.MkdirAll(j.LogDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	stdout, err := os.OpenFile(filepath.Join(j.LogDir, "stdout.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open stdout log: %w", err)
	}
	stderr, err := os.OpenFile(filepath.Join(j.LogDir, "stderr.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		stdout.Close()
		return fmt.Errorf("open stderr log: %w", err)
	}

	cmd := exec.Command(j.Command, j.Args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = nil
	cmd.Dir = j.WorkingDir
	cmd.Env = mergedEnv(j.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return fmt.Errorf("exec: %w", err)
	}

	pid := cmd.Process.Pid
	now := time.Now()
	hash := j.Hash
	nice := j.Nice

	if err := r.registry.Update(hash, func(j *job.Job) {
		j.PID = pid
		j.StartedAt = now
		j.State = job.Running
		j.ExitedAt = time.Time{}
		j.ExitCode = 0
	}); err != nil {
		stdout.Close()
		stderr.Close()
		return fmt.Errorf("update registry: %w", err)
	}

	if nice != 0 {
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, pid, nice); err != nil {
			slog.Warn("setpriority failed", "pid", pid, "nice", nice, "err", err)
		}
	}

	r.mu.Lock()
	r.procs[hash] = cmd
	r.mu.Unlock()

	if err := r.save(); err != nil {
		slog.Warn("save after start", "err", err)
	}

	r.watchers.Add(1)
	go func() {
		defer r.watchers.Done()
		r.watch(hash, cmd, stdout, stderr)
	}()

	return nil
}

// Wait blocks until every in-flight watch goroutine has exited. Daemon
// shutdown calls this after Stop'ing all running jobs.
func (r *Runner) Wait() {
	r.watchers.Wait()
}

// StopAll signals every currently-running job. The watchers exit on
// their own once cmd.Wait returns.
func (r *Runner) StopAll() {
	for _, j := range r.registry.Snapshot() {
		if j.State == job.Running {
			_ = r.Stop(j.Hash)
		}
	}
}

func (r *Runner) watch(hash string, cmd *exec.Cmd, stdout, stderr *os.File) {
	defer stdout.Close()
	defer stderr.Close()

	err := cmd.Wait()

	r.mu.Lock()
	delete(r.procs, hash)
	r.mu.Unlock()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	now := time.Now()

	var name, shortID string
	var finalState job.State
	_ = r.registry.Update(hash, func(j *job.Job) {
		j.ExitCode = exitCode
		j.ExitedAt = now
		// Cancelled wins regardless of exit code: Stop sets it BEFORE
		// signalling, and an in-flight clean exit must not clobber the
		// user's intent.
		switch {
		case j.State == job.Cancelled:
			// keep
		case exitCode == 0:
			j.State = job.Completed
		default:
			j.State = job.Failed
		}
		name = j.Name
		shortID = j.ShortID()
		finalState = j.State
	})

	if err != nil {
		slog.Info("job exited", "id", shortID, "name", name, "code", exitCode, "err", err)
	} else {
		slog.Info("job exited", "id", shortID, "name", name, "code", exitCode)
	}

	if err := r.save(); err != nil {
		slog.Warn("save after exit", "err", err)
	}

	if r.onExit != nil {
		r.onExit(hash, exitCode, finalState)
	}
}

// Stop signals the running process for hash with SIGTERM. Sets job state
// to Cancelled BEFORE signalling so the watcher does not clobber it with
// Completed/Failed when the process exits.
//
// If the job is Pending (registered but not yet started) it transitions
// straight to Cancelled. If it has already exited, Stop is a no-op.
//
// SIGKILL escalation is intentionally not part of v1; if SIGTERM does not
// land within a few seconds, the user can re-issue Stop. (TODO.md)
func (r *Runner) Stop(hash string) error {
	r.mu.Lock()
	cmd, isRunning := r.procs[hash]
	r.mu.Unlock()

	if !isRunning {
		return r.registry.Update(hash, func(j *job.Job) {
			if j.State == job.Pending {
				j.State = job.Cancelled
			}
		})
	}

	if err := r.registry.Update(hash, func(j *job.Job) {
		if j.State == job.Running {
			j.State = job.Cancelled
		}
	}); err != nil {
		return err
	}

	// Negative pid = process group, because the runner started the child
	// with Setsid. This signals the child AND any grandchildren it forked.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("sigterm pgid %d: %w", cmd.Process.Pid, err)
	}
	return nil
}

// SetOnExit installs a callback that fires after the watch goroutine has
// recorded a child's terminal state. The daemon uses this to apply the
// restart policy without baking policy decisions into the runner.
func (r *Runner) SetOnExit(fn func(hash string, exitCode int, state job.State)) {
	r.onExit = fn
}

func (r *Runner) save() error {
	if r.saveFn == nil {
		return nil
	}
	return r.saveFn()
}

func mergedEnv(extra map[string]string) []string {
	out := os.Environ()
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
