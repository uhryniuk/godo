package daemon

import (
	"errors"
	"io"
	"sync"
)

// InputMerger drains N source channels into a single io.Writer (the PTY
// master). v1 only ever has 0 or 1 active source — the attached client.
// The list-of-sources shape exists from day one so v2's `godo pipe A B`
// can register a sending job's output as another source without touching
// the write path.
//
// Writes are serialized by writeMu so each Send chunk reaches the
// underlying writer atomically. Ordering BETWEEN sources is undefined,
// which is the pueue/socat-style "lines from N senders interleave at
// chunk boundaries" semantic; ordering WITHIN one source is preserved.
type InputMerger struct {
	out     io.Writer
	writeMu sync.Mutex // serializes Writes to out

	sourcesMu sync.Mutex
	sources   map[*InputSource]struct{}
}

// InputSource is one client of an InputMerger. Send to enqueue bytes;
// Close to detach. Both are safe across goroutines and idempotent.
type InputSource struct {
	parent   *InputMerger
	ch       chan []byte   // never closed; sends select against shutdown
	shutdown chan struct{} // closed by Close to signal everyone
	done     chan struct{} // closed by drain goroutine on exit
	once     sync.Once
}

// NewInputMerger constructs a merger that writes to out.
func NewInputMerger(out io.Writer) *InputMerger {
	return &InputMerger{
		out:     out,
		sources: make(map[*InputSource]struct{}),
	}
}

// AddSource registers a new source. The returned source's drain
// goroutine is already running.
func (m *InputMerger) AddSource() *InputSource {
	src := &InputSource{
		parent:   m,
		ch:       make(chan []byte, 32),
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
	}
	m.sourcesMu.Lock()
	m.sources[src] = struct{}{}
	m.sourcesMu.Unlock()
	go src.run()
	return src
}

// CloseAll removes every active source and waits for their drain
// goroutines to finish. Used by daemon shutdown.
func (m *InputMerger) CloseAll() {
	m.sourcesMu.Lock()
	srcs := make([]*InputSource, 0, len(m.sources))
	for src := range m.sources {
		srcs = append(srcs, src)
	}
	m.sourcesMu.Unlock()
	for _, src := range srcs {
		src.Close()
	}
}

func (s *InputSource) run() {
	defer close(s.done)
	for {
		select {
		case chunk := <-s.ch:
			s.parent.writeMu.Lock()
			_, _ = s.parent.out.Write(chunk)
			s.parent.writeMu.Unlock()
		case <-s.shutdown:
			// Drain anything already buffered before exiting so a final
			// Send that beat the Close still gets through.
			for {
				select {
				case chunk := <-s.ch:
					s.parent.writeMu.Lock()
					_, _ = s.parent.out.Write(chunk)
					s.parent.writeMu.Unlock()
				default:
					return
				}
			}
		}
	}
}

// Send enqueues p for the underlying writer. p is copied so the caller
// may reuse it. Returns an error if Close was called before Send started;
// callers that race Send against Close in concurrent goroutines may
// occasionally see a successful return whose chunk is then dropped.
func (s *InputSource) Send(p []byte) error {
	// Fast-path the post-Close case so the error is deterministic when
	// the caller serializes Send/Close.
	select {
	case <-s.shutdown:
		return errors.New("input source closed")
	default:
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)
	select {
	case s.ch <- chunk:
		return nil
	case <-s.shutdown:
		return errors.New("input source closed")
	}
}

// Close detaches the source from its merger and waits for the drain
// goroutine to finish flushing what it had buffered. Idempotent.
func (s *InputSource) Close() {
	s.once.Do(func() {
		close(s.shutdown)
	})
	<-s.done
	s.parent.sourcesMu.Lock()
	delete(s.parent.sources, s)
	s.parent.sourcesMu.Unlock()
}
