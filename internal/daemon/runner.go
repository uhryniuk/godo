package daemon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/uhryniuk/godo/internal/job"
)

// outputLogName is the basename of the per-job combined log file. Single
// file because PTY merges stdout and stderr at the kernel level.
const outputLogName = "output.log"

// runningProc bundles all live state for one supervised process.
type runningProc struct {
	cmd     *exec.Cmd
	ptmx    *os.File     // PTY master; child's stdio is bound to the slave
	mux     *Multiplexer // fanout for PTY-master reads
	merger  *InputMerger // fan-in to PTY master from N input sources
	logFile *os.File     // per-job output.log; subscribed to mux

	readerDone chan struct{} // closed when the PTY reader goroutine exits
	writerDone chan struct{} // closed when the log writer goroutine exits
	done       chan struct{} // closed at the end of watch(), after registry update — restart waits on this
}

// Runner spawns and supervises child processes. One Runner per daemon.
type Runner struct {
	registry *Registry
	saveFn   func() error // called after every state transition
	onExit   func(hash string, exitCode int, state job.State)

	mu              sync.Mutex
	procs           map[string]*runningProc
	stoppedManually map[string]bool // user requested Stop; cleared on next Start
	watchers        sync.WaitGroup
}

// NewRunner constructs a Runner. saveFn is invoked after every job state
// transition; pass a no-op if you don't need persistence.
func NewRunner(reg *Registry, saveFn func() error) *Runner {
	return &Runner{
		registry:        reg,
		saveFn:          saveFn,
		procs:           make(map[string]*runningProc),
		stoppedManually: make(map[string]bool),
	}
}

// WasStopped reports whether Stop was called for hash since the last
// Start. The reaper consults this so a restart-policy spawn cannot beat
// a user-issued Stop into the procs map.
func (r *Runner) WasStopped(hash string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stoppedManually[hash]
}

// Multiplexer returns the live mux for hash, or nil if the job is not
// running. Used by the Logs streaming handler to subscribe.
func (r *Runner) Multiplexer(hash string) *Multiplexer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rp, ok := r.procs[hash]; ok {
		return rp.mux
	}
	return nil
}

// InputMerger returns the live input merger for hash, or nil if the job
// is not running. Used by the Attach handler to register an input source.
func (r *Runner) InputMerger(hash string) *InputMerger {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rp, ok := r.procs[hash]; ok {
		return rp.merger
	}
	return nil
}

// Resize updates the window size of hash's PTY. Returns ErrNotFound if
// the job is not running. Holds the procs lock so the watcher can't
// close ptmx underneath us.
func (r *Runner) Resize(hash string, cols, rows uint16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rp, ok := r.procs[hash]
	if !ok {
		return ErrNotFound
	}
	return pty.Setsize(rp.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// Start spawns j's process under a PTY. Returns once exec succeeds (PID
// known) or with an error if exec failed. The PTY reader, log writer,
// and exit watcher all run as goroutines tracked on the Runner.
//
// j must already be registered in r.registry before calling Start.
//
// Start clears any prior stoppedManually flag for j.Hash so explicit
// restarts (or new auto-restarts after a fresh user Run) can proceed.
func (r *Runner) Start(j *job.Job) error {
	if !j.State.CanTransition(job.Running) {
		return fmt.Errorf("cannot start job in state %s", j.State)
	}

	r.mu.Lock()
	delete(r.stoppedManually, j.Hash)
	r.mu.Unlock()

	if err := os.MkdirAll(j.LogDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(j.LogDir, outputLogName),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open output log: %w", err)
	}

	cmd := exec.Command(j.Command, j.Args...)
	cmd.Dir = j.WorkingDir
	cmd.Env = mergedEnv(j.Env)
	// pty.Start sets SysProcAttr to {Setsid: true, Setctty: true, Ctty: 0}
	// so we don't need to configure them here.

	ptmx, err := pty.Start(cmd)
	if err != nil {
		logFile.Close()
		return fmt.Errorf("pty.Start: %w", err)
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
		_ = ptmx.Close()
		logFile.Close()
		return fmt.Errorf("update registry: %w", err)
	}

	if nice != 0 {
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, pid, nice); err != nil {
			slog.Warn("setpriority failed", "pid", pid, "nice", nice, "err", err)
		}
	}

	mux := NewMultiplexer(64)
	logSub := mux.Subscribe()
	merger := NewInputMerger(ptmx)

	rp := &runningProc{
		cmd:        cmd,
		ptmx:       ptmx,
		mux:        mux,
		merger:     merger,
		logFile:    logFile,
		readerDone: make(chan struct{}),
		writerDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	r.mu.Lock()
	r.procs[hash] = rp
	r.mu.Unlock()

	// PTY reader: drain ptmx into mux until EOF (child exit closes slave).
	go func() {
		defer close(rp.readerDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = mux.Write(buf[:n])
			}
			if err != nil {
				// EOF on child exit; EIO on Linux when slave closes.
				return
			}
		}
	}()

	// Log writer: drain mux subscription to disk. Exits when mux closes.
	go func() {
		defer close(rp.writerDone)
		defer logFile.Close()
		for chunk := range logSub.Ch {
			if _, err := logFile.Write(chunk); err != nil {
				slog.Warn("write log", "err", err)
				// Drop the rest; don't tear down the job for a bad disk.
				for range logSub.Ch {
				}
				return
			}
		}
	}()

	if err := r.save(); err != nil {
		slog.Warn("save after start", "err", err)
	}

	r.watchers.Add(1)
	go func() {
		defer r.watchers.Done()
		r.watch(hash, rp)
	}()

	return nil
}

func (r *Runner) watch(hash string, rp *runningProc) {
	err := rp.cmd.Wait()

	// Prefer letting the PTY reader exit naturally so we don't truncate
	// any final buffered output. On some kernels the master read can
	// block past child exit; force-close as a backstop after a brief
	// grace period. We do not call ptmx.Close() yet — handleAttach
	// might still hold rp via the procs map and call Resize on ptmx.
	// Close after deleting from procs (under the same lock).
	select {
	case <-rp.readerDone:
	case <-time.After(100 * time.Millisecond):
		// PTY reader is wedged; force the read to return. Close holds
		// no rp-related locks so this is safe.
		_ = rp.ptmx.Close()
		<-rp.readerDone
	}

	// Tear down fanout: signals the log writer to drain and exit, plus
	// any logs-follow subscribers see channel close.
	rp.mux.Close()
	<-rp.writerDone

	// Tear down input side: any attached client's source goroutine
	// stops; CloseAll waits for buffered input to flush before returning.
	rp.merger.CloseAll()

	// Pull rp out of procs FIRST, then close ptmx. Anyone holding the
	// procs lock has either seen rp (and finished any ptmx operation
	// before releasing) or no longer sees it.
	r.mu.Lock()
	delete(r.procs, hash)
	r.mu.Unlock()
	_ = rp.ptmx.Close()

	exitCode := 0
	if rp.cmd.ProcessState != nil {
		exitCode = rp.cmd.ProcessState.ExitCode()
	}
	now := time.Now()

	var name, shortID string
	var finalState job.State
	_ = r.registry.Update(hash, func(j *job.Job) {
		j.ExitCode = exitCode
		j.ExitedAt = now
		switch {
		case j.State == job.Cancelled:
			// keep — Stop set this before signalling
		case exitCode == 0:
			j.State = job.Completed
		default:
			j.State = job.Failed
		}
		name = j.Name
		shortID = j.ShortID()
		finalState = j.State
	})

	if err != nil && !isExpectedExitErr(err) {
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

	// Closed last so any waiter (notably handleRestart's WaitExit) sees the
	// terminal registry state once it unblocks.
	close(rp.done)
}

// isExpectedExitErr returns true for errors that are normal in our
// shutdown paths and not worth logging as a fault.
func isExpectedExitErr(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true
	}
	return errors.Is(err, io.EOF)
}

// WaitExit returns a channel that closes when hash's running proc has
// fully exited and its watcher has recorded the terminal state, plus
// true if there's currently a running proc to wait on. Returns
// (nil, false) if the proc isn't in the procs map (already exited or
// never started). Used by handleRestart to gate the respawn on actual
// process death rather than registry-state polling.
func (r *Runner) WaitExit(hash string) (<-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rp, ok := r.procs[hash]
	if !ok {
		return nil, false
	}
	return rp.done, true
}

// Kill sends SIGKILL to the running proc's process group. No-op if not
// running. Used as escalation when SIGTERM didn't land in time.
// Doesn't touch registry state — Stop already set it to Cancelled.
func (r *Runner) Kill(hash string) error {
	r.mu.Lock()
	rp, ok := r.procs[hash]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if err := syscall.Kill(-rp.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("sigkill pgid %d: %w", rp.cmd.Process.Pid, err)
	}
	return nil
}

// Stop signals the running process for hash with SIGTERM. Sets job state
// to Cancelled BEFORE signalling so the watcher does not clobber it with
// Completed/Failed when the process exits.
//
// If the job is Pending (registered but not yet started) it transitions
// straight to Cancelled. If it has already exited, Stop is a no-op.
//
// Stop returns once the signal is delivered; it does not wait for the
// child to actually exit. Callers that need that guarantee (e.g. restart)
// should pair Stop with WaitExit and escalate to Kill on timeout.
func (r *Runner) Stop(hash string) error {
	r.mu.Lock()
	r.stoppedManually[hash] = true
	rp, isRunning := r.procs[hash]
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

	// Negative pid = process group, because pty.Start sets Setsid. Hits
	// the child AND any grandchildren it forked.
	if err := syscall.Kill(-rp.cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("sigterm pgid %d: %w", rp.cmd.Process.Pid, err)
	}
	return nil
}

// SetOnExit installs a callback that fires after the watch goroutine has
// recorded a child's terminal state. The daemon uses this to apply the
// restart policy without baking policy decisions into the runner.
func (r *Runner) SetOnExit(fn func(hash string, exitCode int, state job.State)) {
	r.onExit = fn
}

// Wait blocks until every in-flight watch goroutine has exited.
func (r *Runner) Wait() {
	r.watchers.Wait()
}

// StopAll signals every currently-running job. Used by daemon shutdown.
func (r *Runner) StopAll() {
	for _, j := range r.registry.Snapshot() {
		if j.State == job.Running {
			_ = r.Stop(j.Hash)
		}
	}
}

func mergedEnv(extra map[string]string) []string {
	out := os.Environ()
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

func (r *Runner) save() error {
	if r.saveFn == nil {
		return nil
	}
	return r.saveFn()
}
