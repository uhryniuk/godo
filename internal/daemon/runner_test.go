package daemon

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/job"
)

// waitForState polls registry.GetCopy until State equals want or timeout fires.
// Reads via the registry so the loop stays race-free with the watcher goroutine.
func waitForState(t *testing.T, reg *Registry, hash string, want job.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		j, err := reg.GetCopy(hash)
		if err == nil && j.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := reg.GetCopy(hash)
	t.Fatalf("job %s did not reach state %s within %s (current=%s)", hash[:8], want, timeout, j.State)
}

func newTestRunner(t *testing.T) (*Runner, *Registry) {
	t.Helper()
	reg := NewRegistry()
	r := NewRunner(reg, func() error { return nil })
	return r, reg
}

func TestRunnerRunsAndCompletes(t *testing.T) {
	r, reg := newTestRunner(t)
	j := job.New("/bin/sh", []string{"-c", "exit 0"},
		job.WithLogDir(filepath.Join(t.TempDir(), "log")))
	if err := reg.Add(j); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Start(j); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, _ := reg.GetCopy(j.Hash)
	if got.PID == 0 {
		t.Error("PID should be set after Start")
	}
	waitForState(t, reg, j.Hash, job.Completed, 3*time.Second)
	got, _ = reg.GetCopy(j.Hash)
	if got.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", got.ExitCode)
	}
	if got.ExitedAt.IsZero() {
		t.Error("ExitedAt should be set")
	}
}

func TestRunnerCapturesNonZeroExit(t *testing.T) {
	r, reg := newTestRunner(t)
	j := job.New("/bin/sh", []string{"-c", "exit 7"},
		job.WithLogDir(filepath.Join(t.TempDir(), "log")))
	_ = reg.Add(j)
	if err := r.Start(j); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, reg, j.Hash, job.Failed, 3*time.Second)
	got, _ := reg.GetCopy(j.Hash)
	if got.ExitCode != 7 {
		t.Errorf("exit code: got %d, want 7", got.ExitCode)
	}
}

func TestRunnerWritesStdoutAndStderr(t *testing.T) {
	r, reg := newTestRunner(t)
	logDir := filepath.Join(t.TempDir(), "log")
	j := job.New("/bin/sh", []string{"-c", "echo to-stdout; echo to-stderr 1>&2"},
		job.WithLogDir(logDir))
	_ = reg.Add(j)
	if err := r.Start(j); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, reg, j.Hash, job.Completed, 3*time.Second)

	stdout, err := os.ReadFile(filepath.Join(logDir, "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout.log: %v", err)
	}
	if string(stdout) != "to-stdout\n" {
		t.Errorf("stdout: got %q, want %q", stdout, "to-stdout\n")
	}
	stderr, err := os.ReadFile(filepath.Join(logDir, "stderr.log"))
	if err != nil {
		t.Fatalf("read stderr.log: %v", err)
	}
	if string(stderr) != "to-stderr\n" {
		t.Errorf("stderr: got %q, want %q", stderr, "to-stderr\n")
	}
}

func TestRunnerInheritsAndOverridesEnv(t *testing.T) {
	r, reg := newTestRunner(t)
	logDir := filepath.Join(t.TempDir(), "log")
	j := job.New("/bin/sh", []string{"-c", "echo $GODO_TEST_KEY"},
		job.WithLogDir(logDir),
		job.WithEnv(map[string]string{"GODO_TEST_KEY": "value-set"}))
	_ = reg.Add(j)
	if err := r.Start(j); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, reg, j.Hash, job.Completed, 3*time.Second)
	stdout, err := os.ReadFile(filepath.Join(logDir, "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if string(stdout) != "value-set\n" {
		t.Errorf("env: got %q, want %q", stdout, "value-set\n")
	}
}

func TestRunnerFailsOnUnknownCommand(t *testing.T) {
	r, reg := newTestRunner(t)
	j := job.New("/no/such/binary/godo-test", nil,
		job.WithLogDir(filepath.Join(t.TempDir(), "log")))
	_ = reg.Add(j)
	err := r.Start(j)
	if err == nil {
		t.Fatal("expected error from Start, got nil")
	}
	got, _ := reg.GetCopy(j.Hash)
	if got.State != job.Pending {
		t.Errorf("state should remain Pending on Start failure, got %s", got.State)
	}
}

func TestRunnerCallsSaveAfterStartAndExit(t *testing.T) {
	reg := NewRegistry()
	var saveCount int32
	r := NewRunner(reg, func() error {
		atomic.AddInt32(&saveCount, 1)
		return nil
	})

	j := job.New("/bin/sh", []string{"-c", "exit 0"},
		job.WithLogDir(filepath.Join(t.TempDir(), "log")))
	_ = reg.Add(j)
	if err := r.Start(j); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, reg, j.Hash, job.Completed, 3*time.Second)
	if got := atomic.LoadInt32(&saveCount); got < 2 {
		t.Errorf("saveFn calls: got %d, want >= 2", got)
	}
}
