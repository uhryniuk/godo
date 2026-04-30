package daemon

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// drain reads from sub.Ch into a single buffer until close, with a
// timeout that fails the test if the channel never closes.
func drain(t *testing.T, sub *Subscriber, timeout time.Duration) []byte {
	t.Helper()
	var got bytes.Buffer
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case chunk, ok := <-sub.Ch:
			if !ok {
				return got.Bytes()
			}
			got.Write(chunk)
		case <-deadline.C:
			t.Fatalf("subscriber channel did not close within %s", timeout)
		}
	}
}

func TestMultiplexerFanOutPreservesOrder(t *testing.T) {
	m := NewMultiplexer(64)
	a := m.Subscribe()
	b := m.Subscribe()
	c := m.Subscribe()

	chunks := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, s := range chunks {
		if _, err := m.Write([]byte(s)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	m.Close()

	want := "alphabetagammadeltaepsilon"
	for i, sub := range []*Subscriber{a, b, c} {
		if got := string(drain(t, sub, time.Second)); got != want {
			t.Errorf("subscriber %d: got %q, want %q", i, got, want)
		}
	}
}

func TestMultiplexerSubscribeMidStreamSkipsPriorBytes(t *testing.T) {
	m := NewMultiplexer(64)

	if _, err := m.Write([]byte("before")); err != nil {
		t.Fatalf("write: %v", err)
	}

	late := m.Subscribe()
	if _, err := m.Write([]byte("after")); err != nil {
		t.Fatalf("write: %v", err)
	}
	m.Close()

	if got := string(drain(t, late, time.Second)); got != "after" {
		t.Errorf("late subscriber: got %q, want %q", got, "after")
	}
}

func TestMultiplexerCancelStopsDelivery(t *testing.T) {
	m := NewMultiplexer(64)
	keep := m.Subscribe()
	gone := m.Subscribe()

	if _, err := m.Write([]byte("first")); err != nil {
		t.Fatalf("write: %v", err)
	}
	gone.Cancel()
	if _, err := m.Write([]byte("second")); err != nil {
		t.Fatalf("write: %v", err)
	}
	m.Close()

	if got := string(drain(t, keep, time.Second)); got != "firstsecond" {
		t.Errorf("keep: got %q, want %q", got, "firstsecond")
	}
	if got := string(drain(t, gone, time.Second)); got != "first" {
		t.Errorf("gone (after cancel): got %q, want %q", got, "first")
	}
}

func TestMultiplexerCancelIsIdempotent(t *testing.T) {
	m := NewMultiplexer(64)
	s := m.Subscribe()
	s.Cancel()
	s.Cancel() // must not panic / double-close
	m.Close()  // must not panic on already-cancelled sub
}

func TestMultiplexerCloseClosesAllChannels(t *testing.T) {
	m := NewMultiplexer(64)
	a := m.Subscribe()
	b := m.Subscribe()
	m.Close()

	for _, sub := range []*Subscriber{a, b} {
		select {
		case _, ok := <-sub.Ch:
			if ok {
				t.Errorf("expected closed channel, got value")
			}
		default:
			t.Errorf("expected closed channel, got block")
		}
	}
}

func TestMultiplexerWriteAfterCloseIsNoop(t *testing.T) {
	m := NewMultiplexer(64)
	m.Close()
	n, err := m.Write([]byte("anything"))
	if err != nil {
		t.Fatalf("write after close: %v", err)
	}
	if n != len("anything") {
		t.Errorf("write returned %d, want %d", n, len("anything"))
	}
}

func TestMultiplexerSlowSubscriberDoesNotBlockWriter(t *testing.T) {
	// Bug we are guarding against: a subscriber that never drains its
	// channel must not stall the PTY reader. With drop-on-full policy,
	// 100 writes through a tiny buffer must complete in microseconds
	// and the slow subscriber's Dropped counter should rise.
	m := NewMultiplexer(2)
	slow := m.Subscribe()
	defer slow.Cancel()
	// Slow is never drained.

	start := time.Now()
	for i := 0; i < 100; i++ {
		if _, err := m.Write([]byte{byte(i)}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	m.Close()

	if elapsed > 100*time.Millisecond {
		t.Errorf("100 writes took %s — slow subscriber appears to be blocking the writer", elapsed)
	}
	if d := slow.Dropped(); d == 0 {
		t.Errorf("slow subscriber should have non-zero Dropped, got 0")
	}
}

func TestMultiplexerConcurrentWritesAndSubscribes(t *testing.T) {
	// Race-detector smoke: many writers, many subscribers, many
	// add/cancel cycles. Just check no panics and no -race violations.
	m := NewMultiplexer(64)

	var wg sync.WaitGroup
	const writers = 8
	const writes = 200
	var totalWritten uint64

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writes; i++ {
				if _, err := m.Write([]byte{byte(i)}); err != nil {
					t.Errorf("write: %v", err)
				}
				atomic.AddUint64(&totalWritten, 1)
			}
		}()
	}

	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				sub := m.Subscribe()
				go func() {
					for range sub.Ch {
					}
				}()
				time.Sleep(time.Microsecond)
				sub.Cancel()
			}
		}()
	}

	wg.Wait()
	m.Close()
}

func TestMultiplexerCopyIsolatesCallerBuffer(t *testing.T) {
	// If the caller reuses its slice, the subscriber should still see
	// the original bytes.
	m := NewMultiplexer(64)
	sub := m.Subscribe()

	buf := []byte{1, 2, 3, 4, 5}
	if _, err := m.Write(buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Mutate the caller buffer.
	for i := range buf {
		buf[i] = 0
	}
	m.Close()

	got := drain(t, sub, time.Second)
	want := []byte{1, 2, 3, 4, 5}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v (caller mutation leaked through)", got, want)
	}
}
