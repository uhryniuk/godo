package daemon

import (
	"context"
	"os"
	"path/filepath"
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

func TestLogsReturnsStdoutAndStderr(t *testing.T) {
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
	if logs.Stdout != "out-line\n" {
		t.Errorf("stdout: got %q, want %q", logs.Stdout, "out-line\n")
	}
	if logs.Stderr != "err-line\n" {
		t.Errorf("stderr: got %q, want %q", logs.Stderr, "err-line\n")
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
