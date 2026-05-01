package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
)

func waitForJobState(t *testing.T, c *proto.Client, hash string, want job.State, timeout time.Duration) job.Job {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		list, err := c.List(ctx)
		if err == nil {
			for _, j := range list.Jobs {
				if j.Hash == hash && j.State == want {
					return j
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach state %s within %s", hash[:8], want, timeout)
	return job.Job{}
}

func TestStopSetsCancelled(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := c.Stop(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := waitForJobState(t, c, resp.Job.Hash, job.Cancelled, 3*time.Second)
	if got.PID == 0 {
		t.Error("PID should still be recorded for the cancelled job")
	}
}

func TestStopIsIdempotentForExitedJob(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 0"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitForJobState(t, c, resp.Job.Hash, job.Completed, 2*time.Second)

	// Stop on a completed job should not error.
	if _, err := c.Stop(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("stop on completed: %v", err)
	}
}

func TestRemoveDeletesLogDirAndRegistryEntry(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hi; exit 0"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitForJobState(t, c, resp.Job.Hash, job.Completed, 2*time.Second)

	if _, err := os.Stat(resp.Job.LogDir); err != nil {
		t.Fatalf("log dir should exist before remove: %v", err)
	}
	if _, err := c.Remove(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(resp.Job.LogDir); !os.IsNotExist(err) {
		t.Errorf("log dir should be gone after remove: stat=%v", err)
	}
	list, _ := c.List(ctx)
	if len(list.Jobs) != 0 {
		t.Errorf("registry should be empty after remove, got %d jobs", len(list.Jobs))
	}
}

func TestRemoveRefusesRunningJob(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	_, err = c.Remove(ctx, resp.Job.Hash)
	if err == nil {
		t.Fatal("expected Remove to refuse a running job")
	}
}

func TestRestartPolicyOnFailureLoops(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 1"},
		Restart: string(job.RestartOnFailure),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	// Each iteration is sub-millisecond; we should see many restarts in 1s.
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		for _, j := range list.Jobs {
			if j.Hash == resp.Job.Hash {
				got = j.RestartCount
			}
		}
		if got >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got < 3 {
		t.Errorf("RestartCount: got %d, want >= 3", got)
	}
}

func TestRestartPolicyNoDoesNotRestart(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 1"},
		Restart: string(job.RestartNo),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	got := waitForJobState(t, c, resp.Job.Hash, job.Failed, 2*time.Second)
	if got.RestartCount != 0 {
		t.Errorf("RestartCount: got %d, want 0", got.RestartCount)
	}
}

func TestExplicitRestartUsesSameHash(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	originalPID := resp.Job.PID
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	rresp, err := c.Restart(ctx, resp.Job.Hash)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rresp.Job.Hash != resp.Job.Hash {
		t.Errorf("restart should preserve hash: got %s, want %s", rresp.Job.Hash, resp.Job.Hash)
	}
	if rresp.Job.PID == originalPID {
		t.Errorf("restart should give a new PID: still %d", originalPID)
	}
	if rresp.Job.State != job.Running {
		t.Errorf("post-restart state: got %s, want running", rresp.Job.State)
	}
}

// pidAlive returns true if pid is still a live process. Uses kill(0) which
// is the standard "exists?" probe — ESRCH means the process is gone.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}

// TestRestartOldPIDFullyExited covers the bug where restart would mark the
// new job Running while the old PID was still alive (rogue webserver still
// serving on its port). Restart must return only after the predecessor is
// fully reaped.
func TestRestartOldPIDFullyExited(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	oldPID := resp.Job.PID
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	rresp, err := c.Restart(ctx, resp.Job.Hash)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rresp.Job.PID == oldPID {
		t.Fatalf("restart did not produce a new PID")
	}
	// The whole point: the old PID must be dead by the time Restart
	// returns. Allow a tiny grace because reparenting can lag a bit.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && pidAlive(oldPID) {
		time.Sleep(10 * time.Millisecond)
	}
	if pidAlive(oldPID) {
		t.Errorf("old PID %d still alive after restart", oldPID)
	}
}

// TestRestartEscalatesToSIGKILL covers the SIGTERM-resistant case (a
// child that ignores SIGTERM). Restart should escalate to SIGKILL and
// still succeed within the deadline.
func TestRestartEscalatesToSIGKILL(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Echo READY *after* installing the trap. We wait for that line in
	// the job log before calling Restart so the SIGTERM doesn't race with
	// trap installation (which would let the shell die before it ignores
	// the signal — defeating the test).
	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", `trap "" TERM; echo READY; while :; do sleep 1; done`},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	oldPID := resp.Job.PID
	// Cleanup must SIGKILL because c.Stop only sends SIGTERM and the
	// child traps it. Without this, the daemon-shutdown wait would hang.
	defer func() {
		if list, err := c.List(ctx); err == nil {
			for _, j := range list.Jobs {
				if j.Hash == resp.Job.Hash && j.PID > 0 {
					_ = syscall.Kill(-j.PID, syscall.SIGKILL)
				}
			}
		}
	}()

	readyDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(readyDeadline) {
		logs, err := c.Logs(ctx, resp.Job.Hash)
		if err == nil && strings.Contains(logs.Output, "READY") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	start := time.Now()
	rresp, err := c.Restart(ctx, resp.Job.Hash)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if elapsed < 2*time.Second {
		t.Errorf("restart returned too fast (%s) — SIGTERM escalation should have engaged", elapsed)
	}
	if elapsed > 6*time.Second {
		t.Errorf("restart took too long (%s)", elapsed)
	}
	if rresp.Job.PID == oldPID {
		t.Fatalf("restart did not produce a new PID")
	}
	if pidAlive(oldPID) {
		t.Errorf("old PID %d still alive after SIGKILL escalation", oldPID)
	}
}

// TestPauseResumeRoundTrip exercises SIGSTOP/SIGCONT over the wire.
// A paused process can be detected via /proc/<pid>/status: state line
// shows "T (stopped)" while paused and "S (sleeping)" or "R (running)"
// after resume.
func TestPauseResumeRoundTrip(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "while :; do :; done"}, // CPU-busy so /proc state is stable
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()
	pid := resp.Job.PID

	if _, err := c.Pause(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("pause: %v", err)
	}
	got := waitForJobState(t, c, resp.Job.Hash, job.Paused, 2*time.Second)
	if got.PID != pid {
		t.Errorf("PID changed across pause: %d vs %d", got.PID, pid)
	}
	if !procIsStopped(t, pid) {
		t.Errorf("pid %d should be in T (stopped) state after pause", pid)
	}

	if _, err := c.Resume(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitForJobState(t, c, resp.Job.Hash, job.Running, 2*time.Second)
	if procIsStopped(t, pid) {
		t.Errorf("pid %d should not be stopped after resume", pid)
	}
}

// TestStopWorksOnPaused verifies SIGCONT-before-SIGTERM in Stop:
// without it, SIGTERM would queue behind SIGSTOP and the child would
// outlive the request.
func TestStopWorksOnPaused(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 60"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := c.Pause(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitForJobState(t, c, resp.Job.Hash, job.Paused, 2*time.Second)
	if _, err := c.Stop(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForJobState(t, c, resp.Job.Hash, job.Cancelled, 3*time.Second)
}

// procIsStopped reads /proc/<pid>/status and returns true if the state
// line indicates the process is SIGSTOP'd (T = traced/stopped).
func procIsStopped(t *testing.T, pid int) bool {
	t.Helper()
	body, err := os.ReadFile("/proc/" + itoa(pid) + "/status")
	if err != nil {
		t.Fatalf("read /proc/%d/status: %v", pid, err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.Contains(line, "T (stopped)") || strings.Contains(line, "t (tracing")
		}
	}
	return false
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func TestLogsReturnsCombinedOutput(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo out-line; echo err-line 1>&2"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitForJobState(t, c, resp.Job.Hash, job.Completed, 2*time.Second)

	logs, err := c.Logs(ctx, resp.Job.Hash)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	got := strings.ReplaceAll(logs.Output, "\r\n", "\n")
	if !strings.Contains(got, "out-line\n") {
		t.Errorf("stdout missing from output: %q", got)
	}
	if !strings.Contains(got, "err-line\n") {
		t.Errorf("stderr missing from output: %q", got)
	}
}

// TestStopAfterRunBeforeProcessExits is a small race check: spawn a
// command that would exit cleanly, send Stop, and verify the final state
// is Cancelled (not Completed). Flaky-by-design — uses a small sleep so
// Stop wins the race in the common case.
func TestStopBeatsCleanExit(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 0.5; exit 0"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := c.Stop(ctx, resp.Job.Hash); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := waitForJobState(t, c, resp.Job.Hash, job.Cancelled, 2*time.Second)
	_ = got
}

// keep this helper in this file separate from registry_test's
var _ = filepath.Join

func TestShutdownRPCStopsDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "godo.sock")
	d := New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitForSocket(t, sock, 2*time.Second)

	c := proto.NewClient(sock)
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("daemon Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not exit after Shutdown RPC")
	}
}

func TestShutdownStopsRunningChildren(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "godo.sock")
	d := New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitForSocket(t, sock, 2*time.Second)

	c := proto.NewClient(sock)
	if _, err := c.Run(context.Background(), proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit after Shutdown with running child")
	}
}
