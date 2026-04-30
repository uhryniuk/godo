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

	mu    sync.Mutex
	procs map[string]*exec.Cmd // hash -> cmd, for future Stop support
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

	go r.watch(hash, cmd, stdout, stderr)

	return nil
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
	_ = r.registry.Update(hash, func(j *job.Job) {
		j.ExitCode = exitCode
		j.ExitedAt = now
		switch {
		case exitCode == 0:
			j.State = job.Completed
		default:
			// Non-zero (or signal-killed: negative) exit. Step 4's Stop
			// will set Cancelled BEFORE sending the signal, so by the
			// time we get here a "Cancelled" state is already in place
			// and we mustn't clobber it.
			if j.State != job.Cancelled {
				j.State = job.Failed
			}
		}
		name = j.Name
		shortID = j.ShortID()
	})

	if err != nil {
		slog.Info("job exited", "id", shortID, "name", name, "code", exitCode, "err", err)
	} else {
		slog.Info("job exited", "id", shortID, "name", name, "code", exitCode)
	}

	if err := r.save(); err != nil {
		slog.Warn("save after exit", "err", err)
	}
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
