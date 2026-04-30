package daemon

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/proto"
)

// TestLogsFollowNoGapAcrossReplayBoundary is the F13 test from the test
// strategy: no chunk dropped or duplicated when a `logs -f` client
// connects to a job mid-stream. The daemon must hand off cleanly between
// disk-replay and live-mux-forwarding.
func TestLogsFollowNoGapAcrossReplayBoundary(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Prints 1..100 with a small inter-line sleep so the follow connects
	// while the producer is still going, exercising the boundary.
	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "for i in $(seq 1 100); do echo $i; sleep 0.005; done"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Wait for some output to be on disk so replay has work to do.
	time.Sleep(100 * time.Millisecond)

	var (
		got bytes.Buffer
		mu  sync.Mutex
	)
	followCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = c.LogsFollow(followCtx, resp.Job.Hash, func(chunk []byte) error {
		mu.Lock()
		got.Write(chunk)
		// Stop reading as soon as we see 100 — the daemon will keep its
		// connection open until job exit otherwise.
		hasFinal := bytes.Contains(got.Bytes(), []byte("\n100\r\n")) ||
			bytes.Contains(got.Bytes(), []byte("\n100\n"))
		mu.Unlock()
		if hasFinal {
			cancel()
		}
		return nil
	})
	// ctx.Cancel triggers conn close, which makes LogsFollow return an
	// error from ReadFrame. That's expected.
	if err != nil && followCtx.Err() == nil {
		t.Fatalf("LogsFollow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	text := strings.ReplaceAll(got.String(), "\r\n", "\n")
	lines := strings.Split(strings.TrimSpace(text), "\n")

	if len(lines) != 100 {
		var preview []string
		if len(lines) > 5 {
			preview = lines[:3]
			preview = append(preview, "...")
			preview = append(preview, lines[len(lines)-3:]...)
		} else {
			preview = lines
		}
		t.Fatalf("got %d lines, want 100. preview: %v", len(lines), preview)
	}

	// Every line must be its own ordinal — proves no gap, no duplicate.
	for i, line := range lines {
		want := strconv.Itoa(i + 1)
		if line != want {
			t.Errorf("line %d: got %q, want %q", i, line, want)
			break
		}
	}
	_ = fmt.Sprint // keep import
}
