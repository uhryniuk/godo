package daemon

import (
	"sync"
	"sync/atomic"
)

// Multiplexer fans one byte stream out to many Subscribers. Used per job
// to tee the PTY master read loop into the on-disk log file plus any
// active `logs -f` clients (and, in v2, any piped-to job's input).
//
// Slow-subscriber policy: drop with a counter. Writers never block; a
// subscriber that doesn't drain its channel quickly enough simply loses
// data and increments its Dropped counter. This keeps the PTY reader at
// line rate and bounds memory.
type Multiplexer struct {
	bufSize int

	mu          sync.Mutex
	subscribers []*Subscriber
	closed      bool
}

// Subscriber receives chunks. Always read until Ch is closed; otherwise
// you'll cause drops. Cancel removes this subscriber from its parent
// Multiplexer.
type Subscriber struct {
	Ch      chan []byte
	dropped uint64
	mux     *Multiplexer
}

// NewMultiplexer constructs a Multiplexer. bufSize controls the per-
// subscriber channel buffer (in chunks, not bytes).
func NewMultiplexer(bufSize int) *Multiplexer {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &Multiplexer{bufSize: bufSize}
}

// Subscribe registers a new subscriber. The returned subscriber's Ch is
// closed on Cancel or when the Multiplexer is Closed.
func (m *Multiplexer) Subscribe() *Subscriber {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &Subscriber{
		Ch:  make(chan []byte, m.bufSize),
		mux: m,
	}
	if m.closed {
		close(s.Ch)
		return s
	}
	m.subscribers = append(m.subscribers, s)
	return s
}

// SubscribeWithLockedSnapshot returns a subscriber along with a value
// captured under the same lock. Used by `logs -f` to atomically: take
// the current log-file size and start a subscription, with no race
// window where new writes could land in the file but not the channel.
func (m *Multiplexer) SubscribeWithLockedSnapshot(snapshot func() (any, error)) (*Subscriber, any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, err := snapshot()
	if err != nil {
		return nil, nil, err
	}
	s := &Subscriber{
		Ch:  make(chan []byte, m.bufSize),
		mux: m,
	}
	if m.closed {
		close(s.Ch)
		return s, snap, nil
	}
	m.subscribers = append(m.subscribers, s)
	return s, snap, nil
}

// Write fans p out to every subscriber. The bytes are copied so the
// caller may reuse p. Always returns len(p), nil — Multiplexer is
// effectively a sink, never an error source.
//
// Sends are non-blocking (drop on full buffer), so holding the mu for
// the full fanout costs only a few atomic operations per subscriber.
// Cancel and Subscribe take the same lock, so a subscriber's channel
// cannot be closed mid-send.
func (m *Multiplexer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return len(p), nil
	}
	for _, s := range m.subscribers {
		select {
		case s.Ch <- chunk:
		default:
			atomic.AddUint64(&s.dropped, uint64(len(chunk)))
		}
	}
	return len(p), nil
}

// Close terminates every subscriber and rejects future Subscribe calls.
// Idempotent.
func (m *Multiplexer) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, s := range m.subscribers {
		close(s.Ch)
	}
	m.subscribers = nil
}

// Cancel removes the subscriber from its Multiplexer and closes its
// channel. Safe to call from any goroutine. Idempotent.
func (s *Subscriber) Cancel() {
	if s.mux == nil {
		return
	}
	s.mux.mu.Lock()
	defer s.mux.mu.Unlock()
	for i, sub := range s.mux.subscribers {
		if sub == s {
			s.mux.subscribers = append(s.mux.subscribers[:i], s.mux.subscribers[i+1:]...)
			close(s.Ch)
			s.mux = nil
			return
		}
	}
	// Not in the list — either already cancelled or the mux closed.
	s.mux = nil
}

// Dropped returns the count of bytes this subscriber has missed because
// its channel buffer was full at write time.
func (s *Subscriber) Dropped() uint64 {
	return atomic.LoadUint64(&s.dropped)
}
