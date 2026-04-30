package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/uhryniuk/godo/internal/proto"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// startDaemon spins up a Daemon on a temp socket and returns a stop func.
// stop blocks until Run returns.
func startDaemon(t *testing.T) (sock string, stop func()) {
	t.Helper()
	sock = filepath.Join(t.TempDir(), "godo.sock")
	d := New(sock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	waitForSocket(t, sock, 2*time.Second)
	return sock, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon Run returned: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop within 2s of cancel")
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

func TestPing(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if resp.Version != Version {
		t.Errorf("version: got %q want %q", resp.Version, Version)
	}
	if resp.PID != os.Getpid() {
		t.Errorf("pid: got %d want %d", resp.PID, os.Getpid())
	}
}

func TestUnknownOpReturnsError(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := c.Call(ctx, proto.Request{Op: "Bogus"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.OK {
		t.Fatal("expected OK=false for unknown op")
	}
	if resp.Error == "" {
		t.Fatal("expected error string for unknown op")
	}
}

// TestSocketRemovedOnShutdown verifies the daemon cleans up its socket file.
func TestSocketRemovedOnShutdown(t *testing.T) {
	sock, stop := startDaemon(t)
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket should exist while daemon runs: %v", err)
	}
	stop()
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed after shutdown: stat=%v", err)
	}
}

// TestStaleSocketCleared verifies a leftover socket file from a prior run
// does not block startup.
func TestStaleSocketCleared(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "godo.sock")
	// Pre-create a stale file.
	if err := os.WriteFile(sock, []byte("stale"), 0600); err != nil {
		t.Fatalf("create stale: %v", err)
	}

	d := New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitForSocket(t, sock, 2*time.Second)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
