package autospawn_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/daemon"
	"github.com/uhryniuk/godo/internal/proto"
)

func TestMain(m *testing.M) {
	// Shorten the timing tunables so failures don't drag.
	autospawn.PingProbeTimeout = 100 * time.Millisecond
	autospawn.PollInterval = 10 * time.Millisecond
	autospawn.PollTimeout = 2 * time.Second
	goleak.VerifyTestMain(m)
}

// shortSockDir returns a temp dir whose path is short enough for a Unix
// socket on macOS (sockaddr_un.sun_path limit = 103 chars). t.TempDir()
// embeds the full test name in the path, which pushes many names past the
// limit; os.MkdirTemp avoids this.
func shortSockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gd")
	if err != nil {
		t.Fatalf("shortSockDir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startInProcessDaemon launches a Daemon goroutine on sock, waits for the
// listener to be ready, and returns a stop function.
func startInProcessDaemon(t *testing.T, sock string) (stop func()) {
	t.Helper()
	d := daemon.New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitForSocket(t, sock, 2*time.Second)
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon Run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop within 2s")
		}
	}
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear within %s", path, timeout)
}

func TestEnsureRunning_AlreadyUp_DoesNotSpawn(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "godo.sock")
	stop := startInProcessDaemon(t, sock)
	defer stop()

	// Wait for the daemon to be reachable so the fast path succeeds.
	if !proto.NewClient(sock).Reachable(time.Second) {
		t.Fatal("daemon not reachable")
	}

	var spawnCount int32
	spawn := func() error {
		atomic.AddInt32(&spawnCount, 1)
		return nil
	}

	if err := autospawn.EnsureRunning(context.Background(), sock, spawn); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if got := atomic.LoadInt32(&spawnCount); got != 0 {
		t.Fatalf("spawn called %d times, want 0", got)
	}
}

func TestEnsureRunning_DownThenUp_SpawnsOnce(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "godo.sock")

	var stop func()
	defer func() {
		if stop != nil {
			stop()
		}
	}()

	var spawnCount int32
	spawn := func() error {
		atomic.AddInt32(&spawnCount, 1)
		stop = startInProcessDaemon(t, sock)
		return nil
	}

	if err := autospawn.EnsureRunning(context.Background(), sock, spawn); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if got := atomic.LoadInt32(&spawnCount); got != 1 {
		t.Fatalf("spawn called %d times, want 1", got)
	}
	if !proto.NewClient(sock).Reachable(time.Second) {
		t.Fatal("daemon not reachable after EnsureRunning")
	}
}

func TestEnsureRunning_ConcurrentSpawnsExactlyOne(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "godo.sock")

	var spawnCount int32
	var startOnce sync.Once
	var stop func()
	defer func() {
		if stop != nil {
			stop()
		}
	}()

	spawn := func() error {
		atomic.AddInt32(&spawnCount, 1)
		startOnce.Do(func() { stop = startInProcessDaemon(t, sock) })
		return nil
	}

	const N = 10
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = autospawn.EnsureRunning(context.Background(), sock, spawn)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("EnsureRunning[%d]: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&spawnCount); got != 1 {
		t.Errorf("spawn called %d times, want exactly 1 (flock+recheck failed)", got)
	}
}

func TestEnsureRunning_TimesOutWhenSpawnIsSilent(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "godo.sock")
	autospawn.PollTimeout = 200 * time.Millisecond
	defer func() { autospawn.PollTimeout = 2 * time.Second }()

	silent := func() error { return nil }
	err := autospawn.EnsureRunning(context.Background(), sock, silent)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestEnsureRunning_HonorsCanceledContext(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "godo.sock")
	autospawn.PollTimeout = 5 * time.Second // would normally wait 5s
	defer func() { autospawn.PollTimeout = 2 * time.Second }()

	silent := func() error { return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := autospawn.EnsureRunning(ctx, sock, silent)
	if err == nil {
		t.Fatal("expected error from cancelled ctx, got nil")
	}
}
