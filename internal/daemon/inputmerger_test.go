package daemon

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncWriter is a thread-safe bytes.Buffer-equivalent writer for tests.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *syncWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Len()
}

func TestInputMergerSingleSourcePassesThrough(t *testing.T) {
	w := &syncWriter{}
	m := NewInputMerger(w)
	src := m.AddSource()

	if err := src.Send([]byte("hello")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := src.Send([]byte(" world")); err != nil {
		t.Fatalf("send: %v", err)
	}
	src.Close()

	if got := w.String(); got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestInputMergerSendCopiesCallerBuffer(t *testing.T) {
	w := &syncWriter{}
	m := NewInputMerger(w)
	src := m.AddSource()

	buf := []byte{1, 2, 3, 4, 5}
	if err := src.Send(buf); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Mutate buf — drain goroutine must see the originals.
	for i := range buf {
		buf[i] = 0
	}
	src.Close()

	got := w.String()
	want := string([]byte{1, 2, 3, 4, 5})
	if got != want {
		t.Errorf("caller mutation leaked through: got %v, want %v", []byte(got), []byte(want))
	}
}

func TestInputMergerSendAfterCloseErrors(t *testing.T) {
	w := &syncWriter{}
	m := NewInputMerger(w)
	src := m.AddSource()
	src.Close()

	err := src.Send([]byte("late"))
	if err == nil {
		t.Fatal("expected error sending to closed source")
	}
}

func TestInputMergerCloseIsIdempotent(t *testing.T) {
	w := &syncWriter{}
	m := NewInputMerger(w)
	src := m.AddSource()
	src.Close()
	src.Close() // must not panic / double-close
	src.Close()
}

func TestInputMergerMultipleSourcesAtomicChunks(t *testing.T) {
	// Two sources each send a distinguishable chunk many times. We can't
	// assert global ordering, but each chunk must arrive contiguously
	// (no chunk-A bytes interleaved with chunk-B bytes mid-write).
	w := &syncWriter{}
	m := NewInputMerger(w)

	srcA := m.AddSource()
	srcB := m.AddSource()

	const N = 50
	chunkA := bytes.Repeat([]byte("A"), 16)
	chunkB := bytes.Repeat([]byte("B"), 16)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			if err := srcA.Send(chunkA); err != nil {
				t.Errorf("A send: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			if err := srcB.Send(chunkB); err != nil {
				t.Errorf("B send: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	srcA.Close()
	srcB.Close()

	got := w.String()
	if total := len(got); total != 2*N*16 {
		t.Errorf("total bytes: got %d, want %d", total, 2*N*16)
	}
	// Walk in 16-byte windows and verify each is all-A or all-B.
	for i := 0; i < len(got); i += 16 {
		end := i + 16
		if end > len(got) {
			end = len(got)
		}
		window := got[i:end]
		if window != string(chunkA) && window != string(chunkB) {
			t.Errorf("interleaved chunk at offset %d: %q", i, window)
			break
		}
	}
}

func TestInputMergerCloseAllStopsEverySource(t *testing.T) {
	w := &syncWriter{}
	m := NewInputMerger(w)
	a := m.AddSource()
	b := m.AddSource()
	c := m.AddSource()
	_ = a.Send([]byte("a"))
	_ = b.Send([]byte("b"))
	_ = c.Send([]byte("c"))
	m.CloseAll()

	if w.Len() != 3 {
		t.Errorf("expected 3 bytes after CloseAll drain, got %d (%q)", w.Len(), w.String())
	}
	for _, s := range []*InputSource{a, b, c} {
		if err := s.Send([]byte("x")); err == nil {
			t.Errorf("Send after CloseAll should error")
		}
	}
}

func TestInputMergerHandlesConcurrentAddAndCloseRace(t *testing.T) {
	// goleak would catch a leak here; -race would catch a write race.
	w := &syncWriter{}
	m := NewInputMerger(w)
	var wg sync.WaitGroup
	var sent int64

	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src := m.AddSource()
			for j := 0; j < 5; j++ {
				if err := src.Send([]byte("x")); err == nil {
					atomic.AddInt64(&sent, 1)
				}
			}
			src.Close()
		}()
	}
	wg.Wait()

	// Give writes a moment to flush (they happen synchronously on Close
	// but in parallel goroutines).
	time.Sleep(20 * time.Millisecond)
	if got := int64(w.Len()); got != atomic.LoadInt64(&sent) {
		t.Errorf("wrote %d bytes, sent %d", got, sent)
	}
}
