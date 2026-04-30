// Package autospawn brokers between a CLI invocation and a running godo
// daemon. EnsureRunning checks if the daemon is reachable and, if not,
// acquires an exclusive flock and starts one via the supplied SpawnFn.
//
// The flock + post-acquire recheck guarantees exactly one daemon is started
// even when many CLI invocations race in parallel.
package autospawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/uhryniuk/godo/internal/proto"
)

// Tunables. Exposed as variables (not consts) so tests can shorten them.
var (
	// PingProbeTimeout caps a single Ping attempt.
	PingProbeTimeout = 250 * time.Millisecond
	// PollInterval is how often we re-Ping while waiting for a freshly
	// spawned daemon to come up.
	PollInterval = 25 * time.Millisecond
	// PollTimeout caps the total wait for a freshly spawned daemon.
	PollTimeout = 2 * time.Second
)

// SpawnFn launches a new supervisor process. It must return promptly, not
// wait for the daemon to be ready — EnsureRunning polls for that.
type SpawnFn func() error

// EnsureRunning makes sure a godo daemon is reachable on socketPath.
//
// Fast path: if the daemon answers a Ping, return nil immediately.
//
// Slow path: acquire an exclusive flock on socketPath+".lock"; re-check
// reachability (in case another caller spawned while we waited on the
// flock); call spawn; poll the socket until the daemon answers or
// PollTimeout elapses.
func EnsureRunning(ctx context.Context, socketPath string, spawn SpawnFn) error {
	client := proto.NewClient(socketPath)
	if pingOK(ctx, client) {
		return nil
	}

	lockPath := socketPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()

	// Another caller may have spawned a daemon while we were waiting on the
	// flock. Don't double-spawn.
	if pingOK(ctx, client) {
		return nil
	}

	if err := spawn(); err != nil {
		return fmt.Errorf("spawn supervisor: %w", err)
	}

	return pollUntilReachable(ctx, client)
}

func pingOK(ctx context.Context, c *proto.Client) bool {
	probeCtx, cancel := context.WithTimeout(ctx, PingProbeTimeout)
	defer cancel()
	_, err := c.Ping(probeCtx)
	return err == nil
}

func pollUntilReachable(ctx context.Context, c *proto.Client) error {
	deadline := time.Now().Add(PollTimeout)
	for time.Now().Before(deadline) {
		if pingOK(ctx, c) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(PollInterval):
		}
	}
	return errors.New("autospawn: daemon did not become reachable within timeout")
}

// SpawnSupervisor is the production SpawnFn. It re-execs this binary with
// the hidden "supervisor" subcommand, redirects stdio to /dev/null, and
// starts a new session via Setsid so the child survives the caller's exit.
func SpawnSupervisor() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	cmd := exec.Command(exe, "supervisor")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	devNullR, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer devNullR.Close()
	devNullW, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer devNullW.Close()

	cmd.Stdin = devNullR
	cmd.Stdout = devNullW
	cmd.Stderr = devNullW

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start supervisor: %w", err)
	}
	// Intentionally do NOT Wait. The child becomes an orphan reparented to
	// init (PID 1) when the parent process exits.
	_ = cmd.Process.Release()
	return nil
}
