package daemon

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRunnerWritesCombinedOutputLog(t *testing.T) {
	r, reg := newTestRunner(t)
	logDir := filepath.Join(t.TempDir(), "log")
	// PTY merges stdout+stderr at the kernel level — both end up in
	// output.log, in interleaved order.
	j := job.New("/bin/sh", []string{"-c", "echo to-stdout; echo to-stderr 1>&2"},
		job.WithLogDir(logDir))
	_ = reg.Add(j)
	if err := r.Start(j); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, reg, j.Hash, job.Completed, 3*time.Second)

	body, err := os.ReadFile(filepath.Join(logDir, outputLogName))
	if err != nil {
		t.Fatalf("read output.log: %v", err)
	}
	got := normalizeNewlines(string(body))
	// Order is not guaranteed across the two FDs, just both must appear.
	if !strings.Contains(got, "to-stdout\n") {
		t.Errorf("stdout missing from output.log: %q", got)
	}
	if !strings.Contains(got, "to-stderr\n") {
		t.Errorf("stderr missing from output.log: %q", got)
	}
}

// normalizeNewlines collapses CRLF to LF. The PTY puts the slave in
// "cooked" mode by default which translates LF to CRLF on output.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
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
	body, err := os.ReadFile(filepath.Join(logDir, outputLogName))
	if err != nil {
		t.Fatalf("read output.log: %v", err)
	}
	if got := normalizeNewlines(string(body)); got != "value-set\n" {
		t.Errorf("env: got %q, want %q", got, "value-set\n")
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
