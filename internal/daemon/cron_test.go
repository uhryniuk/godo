package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
)

// TestCronTickSpawnsJob is the F17 test: a service with a fast cron
// schedule should produce job entries on every tick. We use @every 200ms
// and wait long enough to see at least 2 firings.
func TestCronTickSpawnsJob(t *testing.T) {
	body := `
name = "ticker"
command = "/bin/sh"
args = ["-c", "echo tick"]

[cron]
schedule = "@every 200ms"
overlap = true
`
	sock, _, stop := startDaemonWithServiceDir(t,
		map[string]string{"ticker.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Wait for ≥2 cron-spawned jobs (names like "ticker@<unix-second>").
	deadline := time.Now().Add(3 * time.Second)
	var seen int
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		seen = 0
		for _, j := range list.Jobs {
			if strings.HasPrefix(j.Name, "ticker@") {
				seen++
			}
		}
		if seen >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if seen < 2 {
		t.Errorf("expected at least 2 cron-fired jobs, saw %d", seen)
	}
}

// TestCronOverlapFalseSuppressesConcurrentRuns ensures that with
// overlap=false (default), a tick is skipped while a prior run is
// still active.
func TestCronOverlapFalseSuppressesConcurrentRuns(t *testing.T) {
	// Each fire sleeps 1s; ticks every 200ms. Without suppression we'd
	// expect ~5 concurrent jobs. With overlap=false we should see 1 at
	// a time, and only 1-2 total within ~700ms.
	body := `
name = "slow"
command = "/bin/sh"
args = ["-c", "sleep 1"]

[cron]
schedule = "@every 200ms"
`
	sock, _, stop := startDaemonWithServiceDir(t,
		map[string]string{"slow.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	time.Sleep(700 * time.Millisecond)
	list, _ := c.List(ctx)
	var concurrentRunning int
	for _, j := range list.Jobs {
		if strings.HasPrefix(j.Name, "slow@") && j.State == job.Running {
			concurrentRunning++
		}
	}
	if concurrentRunning > 1 {
		t.Errorf("overlap=false should allow at most one running instance, got %d", concurrentRunning)
	}
}

// TestCronUnregisterOnRemoveStopsFiring confirms that deleting the
// service file via reload also removes its cron entry.
func TestCronUnregisterOnRemoveStopsFiring(t *testing.T) {
	body := `
name = "doomed"
command = "/bin/sh"
args = ["-c", "echo tick"]

[cron]
schedule = "@every 200ms"
overlap = true
`
	sock, serviceDir, stop := startDaemonWithServiceDir(t,
		map[string]string{"doomed.toml": body})
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Wait for at least one tick.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list, _ := c.List(ctx)
		for _, j := range list.Jobs {
			if strings.HasPrefix(j.Name, "doomed@") {
				goto sawOne
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("never saw the first cron tick")

sawOne:
	// Delete file and reload.
	if err := os.Remove(filepath.Join(serviceDir, "doomed.toml")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := c.ReloadServices(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Snapshot count, wait, snapshot again. Count should NOT grow.
	listBefore, _ := c.List(ctx)
	var beforeCount int
	for _, j := range listBefore.Jobs {
		if strings.HasPrefix(j.Name, "doomed@") {
			beforeCount++
		}
	}

	time.Sleep(500 * time.Millisecond)

	listAfter, _ := c.List(ctx)
	var afterCount int
	for _, j := range listAfter.Jobs {
		if strings.HasPrefix(j.Name, "doomed@") {
			afterCount++
		}
	}
	if afterCount > beforeCount {
		t.Errorf("cron kept firing after reload-removal: %d -> %d", beforeCount, afterCount)
	}
}
