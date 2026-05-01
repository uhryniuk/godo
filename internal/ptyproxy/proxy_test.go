package ptyproxy

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStream is a controllable Stream for testing Run.
type fakeStream struct {
	mu       sync.Mutex
	outbox   bytes.Buffer // what RunOn wrote (i.e. data the daemon would receive)
	resizes  []resizeEvt
	inbox    chan []byte // chunks the test wants to deliver via Read
	closed   atomic.Bool
	closeWG  sync.WaitGroup
}

type resizeEvt struct{ cols, rows uint16 }

func newFakeStream() *fakeStream {
	return &fakeStream{inbox: make(chan []byte, 16)}
}

func (f *fakeStream) Read(p []byte) (int, error) {
	chunk, ok := <-f.inbox
	if !ok {
		return 0, io.EOF
	}
	n := copy(p, chunk)
	if n < len(chunk) {
		// Not handling leftover for the tests we care about.
		panic("fakeStream: caller buffer too small for chunk")
	}
	return n, nil
}

func (f *fakeStream) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outbox.Write(p)
	return len(p), nil
}

func (f *fakeStream) Resize(cols, rows uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, resizeEvt{cols, rows})
	return nil
}

func (f *fakeStream) Close() error {
	if f.closed.CompareAndSwap(false, true) {
		close(f.inbox)
	}
	return nil
}

func (f *fakeStream) outboxString() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outbox.String()
}

// runOnAsync spawns RunOn in a goroutine and returns a chan that fires
// with the return value when it exits.
func runOnAsync(t *testing.T, stream Stream, stdin *os.File, stdout io.Writer) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- RunOn(stream, stdin, stdout)
	}()
	return done
}

func waitForOutbox(t *testing.T, fs *fakeStream, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := fs.outboxString(); got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("outbox timeout: got %q, want %q", fs.outboxString(), want)
}

func TestRunOnForwardsStdinToStream(t *testing.T) {
	stream := newFakeStream()
	stdinR, stdinW, _ := os.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()

	var stdout bytes.Buffer
	done := runOnAsync(t, stream, stdinR, &stdout)

	if _, err := stdinW.Write([]byte("hello")); err != nil {
		t.Fatalf("stdin write: %v", err)
	}
	waitForOutbox(t, stream, "hello", time.Second)

	stream.Close()
	stdinW.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunOn returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunOn did not exit")
	}
}

func TestRunOnDeliversStreamReadToStdout(t *testing.T) {
	stream := newFakeStream()
	stdinR, stdinW, _ := os.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()

	pr, pw, _ := os.Pipe()
	defer pr.Close()

	done := runOnAsync(t, stream, stdinR, pw)

	stream.inbox <- []byte("world\n")

	got := make([]byte, 6)
	pr.SetReadDeadline(time.Now().Add(time.Second))
	n, err := io.ReadFull(pr, got)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if string(got[:n]) != "world\n" {
		t.Errorf("stdout: got %q, want %q", got[:n], "world\n")
	}

	stream.Close()
	stdinW.Close()
	pw.Close()
	<-done
}

func TestRunOnDetachExits(t *testing.T) {
	stream := newFakeStream()
	stdinR, stdinW, _ := os.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()

	var stdout bytes.Buffer
	done := runOnAsync(t, stream, stdinR, &stdout)

	// Send the detach sequence: Ctrl-B then 'd'.
	if _, err := stdinW.Write([]byte{detachKey1, detachKey2}); err != nil {
		t.Fatalf("write detach: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("detach should return nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunOn did not return after detach sequence")
	}

	if !stream.closed.Load() {
		t.Error("Run should have closed the stream on detach")
	}
	if got := stream.outboxString(); got != "" {
		t.Errorf("detach bytes leaked to stream: %q", got)
	}
}

func TestRunOnPrefixThenNonDForwardsBoth(t *testing.T) {
	stream := newFakeStream()
	stdinR, stdinW, _ := os.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()

	var stdout bytes.Buffer
	done := runOnAsync(t, stream, stdinR, &stdout)

	// Ctrl-B then 'x' should forward both bytes (not detach).
	if _, err := stdinW.Write([]byte{detachKey1, 'x'}); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForOutbox(t, stream, string([]byte{detachKey1, 'x'}), time.Second)

	stream.Close()
	stdinW.Close()
	<-done
}

func TestRunOnReturnsOnStreamEOF(t *testing.T) {
	stream := newFakeStream()
	stdinR, stdinW, _ := os.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()

	var stdout bytes.Buffer
	done := runOnAsync(t, stream, stdinR, &stdout)

	// Daemon hangs up.
	stream.Close()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("expected nil/EOF, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunOn did not exit on stream EOF")
	}

	stdinW.Close()
}
