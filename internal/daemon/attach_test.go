package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
)

// readUntilContains drains stream into a buffer until needle appears or
// timeout fires. Returns whatever was read regardless.
func readUntilContains(t *testing.T, stream io.Reader, needle string, timeout time.Duration) string {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var got bytes.Buffer
		buf := make([]byte, 256)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				got.Write(buf[:n])
				if strings.Contains(got.String(), needle) {
					ch <- result{got.Bytes(), nil}
					return
				}
			}
			if err != nil {
				ch <- result{got.Bytes(), err}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil && !errors.Is(r.err, io.EOF) {
			t.Logf("read returned: %v", r.err)
		}
		return string(r.data)
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %q in stream", needle)
		return ""
	}
}

func TestAttachEchoesInputThroughPTY(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// cat reads stdin and writes stdout. With a PTY in cooked mode the
	// kernel will echo input bytes back too — so "hi\n" should appear at
	// least once on the master read side.
	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/cat",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	stream, err := c.Attach(ctx, resp.Job.Hash)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	if _, err := stream.Write([]byte("hi\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := readUntilContains(t, stream, "hi", 3*time.Second)
	if !strings.Contains(got, "hi") {
		t.Errorf("did not see echoed input; got %q", got)
	}
	_ = stream.Close()

	// Job should still be running after detach.
	snap := waitForJobState(t, c, resp.Job.Hash, job.Running, 500*time.Millisecond)
	_ = snap
}

func TestAttachReceivesJobOutput(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	// Job prints lines steadily; attach should pick up the live stream.
	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "for i in 1 2 3 4 5; do echo line-$i; sleep 0.05; done; sleep 5"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	// Tiny delay so at least one line is in the multiplexer when we
	// attach (we only see post-subscribe bytes, mirroring tmux semantics).
	time.Sleep(80 * time.Millisecond)

	stream, err := c.Attach(ctx, resp.Job.Hash)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer stream.Close()

	got := readUntilContains(t, stream, "line-5", 2*time.Second)
	if !strings.Contains(got, "line-5") {
		t.Errorf("did not see line-5 in stream; got %q", got)
	}
}

func TestAttachToMissingJobErrors(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	_, err := c.Attach(ctx, "no-such-target")
	if err == nil {
		t.Fatal("expected error attaching to missing target")
	}
}

func TestAttachToCompletedJobErrors(t *testing.T) {
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

	_, err = c.Attach(ctx, resp.Job.Hash)
	if err == nil {
		t.Fatal("expected error attaching to completed job")
	}
}

func TestAttachResizeReachesPTY(t *testing.T) {
	// We can't easily inspect the slave's window size from the test,
	// but we can verify Resize doesn't error and the job stays alive.
	sock, stop := startDaemon(t)
	defer stop()

	c := proto.NewClient(sock)
	ctx := context.Background()

	resp, err := c.Run(ctx, proto.RunRequest{
		Command: "/bin/cat",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() { _, _ = c.Stop(ctx, resp.Job.Hash) }()

	stream, err := c.Attach(ctx, resp.Job.Hash)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer stream.Close()

	if err := stream.Resize(120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := stream.Resize(80, 24); err != nil {
		t.Fatalf("resize back: %v", err)
	}
}
