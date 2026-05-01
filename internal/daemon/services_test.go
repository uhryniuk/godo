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

// startDaemonWithServiceDir spins up a daemon whose ~/.godo/services
// dir is pre-populated with `files` (basename -> body).
func startDaemonWithServiceDir(t *testing.T, files map[string]string) (sock string, serviceDir string, stop func()) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	services := filepath.Join(root, "services")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.MkdirAll(services, 0o755); err != nil {
		t.Fatalf("mkdir services: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(services, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	sock = filepath.Join(state, "godo.sock")
	d := New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitForSocket(t, sock, 2*time.Second)
	return sock, services, func() {
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

func TestServicesAutostartOnBoot(t *testing.T) {
	body := `
name = "auto"
command = "/bin/sh"
args = ["-c", "sleep 30"]
autostart = true
`
	sock, _, stop := startDaemonWithServiceDir(t, map[string]string{"auto.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Wait for the autostart job to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		for _, j := range list.Jobs {
			if j.Name == "auto" && j.State == job.Running {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("autostart job did not reach running state")
}

func TestServicesNoAutostartLeavesNoJob(t *testing.T) {
	body := `
name = "noauto"
command = "/bin/sh"
args = ["-c", "sleep 30"]
autostart = false
`
	sock, _, stop := startDaemonWithServiceDir(t, map[string]string{"noauto.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Give the boot path time to run, then assert no job exists.
	time.Sleep(200 * time.Millisecond)
	list, _ := c.List(ctx)
	for _, j := range list.Jobs {
		if j.Name == "noauto" {
			t.Errorf("autostart=false but job exists: state=%s", j.State)
		}
	}
}

func TestReloadDetectsAddAndRemove(t *testing.T) {
	startBody := `
name = "first"
command = "/bin/sh"
args = ["-c", "sleep 30"]
autostart = true
`
	sock, serviceDir, stop := startDaemonWithServiceDir(t,
		map[string]string{"first.toml": startBody})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Drop in a second service.
	addBody := `
name = "second"
command = "/bin/sh"
args = ["-c", "sleep 30"]
autostart = true
`
	if err := os.WriteFile(filepath.Join(serviceDir, "second.toml"), []byte(addBody), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Remove the first.
	if err := os.Remove(filepath.Join(serviceDir, "first.toml")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	resp, err := c.ReloadServices(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(resp.Added) != 1 || resp.Added[0].Name != "second" {
		t.Errorf("Added: %v", resp.Added)
	}
	if len(resp.Removed) != 1 {
		t.Errorf("Removed: %v", resp.Removed)
	}

	// The 'second' service should be running; 'first' should be stopped
	// (Cancelled/whatever, just NOT Running anymore).
	deadline := time.Now().Add(2 * time.Second)
	var sawSecond, firstStillRunning bool
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		sawSecond = false
		firstStillRunning = false
		for _, j := range list.Jobs {
			if j.Name == "second" && j.State == job.Running {
				sawSecond = true
			}
			if j.Name == "first" && j.State == job.Running {
				firstStillRunning = true
			}
		}
		if sawSecond && !firstStillRunning {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Errorf("post-reload state: second-running=%v, first-still-running=%v", sawSecond, firstStillRunning)
}

func TestReloadModifiedDoesNotAutoRestart(t *testing.T) {
	body := `
name = "mod"
command = "/bin/sh"
args = ["-c", "sleep 30"]
autostart = true
`
	sock, serviceDir, stop := startDaemonWithServiceDir(t,
		map[string]string{"mod.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Wait for autostart.
	deadline := time.Now().Add(2 * time.Second)
	var origPID int
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		for _, j := range list.Jobs {
			if j.Name == "mod" && j.State == job.Running {
				origPID = j.PID
			}
		}
		if origPID != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if origPID == 0 {
		t.Fatal("mod did not autostart")
	}

	// Edit the file (different content -> different hash).
	newBody := body + "\nnice = 1\n"
	if err := os.WriteFile(filepath.Join(serviceDir, "mod.toml"), []byte(newBody), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	resp, err := c.ReloadServices(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(resp.Modified) != 1 || resp.Modified[0].Name != "mod" {
		t.Errorf("Modified: %v", resp.Modified)
	}

	// PID should NOT have changed — modify is in-memory only, no auto-restart.
	time.Sleep(150 * time.Millisecond)
	list, _ := c.List(ctx)
	for _, j := range list.Jobs {
		if j.Name == "mod" {
			if j.PID != origPID {
				t.Errorf("PID changed after reload: was %d, now %d", origPID, j.PID)
			}
			if j.State != job.Running {
				t.Errorf("modified service should stay Running, got %s", j.State)
			}
			return
		}
	}
	t.Error("mod job disappeared")
}

func TestLoadServiceImportsAndStarts(t *testing.T) {
	sock, _, stop := startDaemonWithServiceDir(t, nil)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "imported.toml")
	if err := os.WriteFile(src, []byte(`
name = "imported"
command = "/bin/sh"
args = ["-c", "sleep 30"]
autostart = true
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := c.LoadService(ctx, src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resp.Service.Name != "imported" {
		t.Errorf("name: got %q", resp.Service.Name)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		for _, j := range list.Jobs {
			if j.Name == "imported" && j.State == job.Running {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("imported service did not reach Running")
}

func TestLoadServiceRefusesDuplicate(t *testing.T) {
	body := `
name = "x"
command = "/bin/true"
`
	sock, _, stop := startDaemonWithServiceDir(t,
		map[string]string{"dup.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "dup.toml")
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := c.LoadService(ctx, src)
	if err == nil {
		t.Fatal("expected error loading duplicate-name file")
	}
}
