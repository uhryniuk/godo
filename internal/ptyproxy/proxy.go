package ptyproxy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// Stream is the bidirectional channel to the daemon. proto.AttachStream
// satisfies it; the interface lives here so this package has no upward
// dependency.
type Stream interface {
	io.Reader
	io.Writer
	Resize(cols, rows uint16) error
	Close() error
}

// Run takes over the calling process's stdin/stdout to proxy to and
// from stream. It puts stdin in raw mode (if it's a tty), forwards
// SIGWINCH events as resize frames, and watches for the Ctrl-B d
// detach sequence. Returns nil on detach or clean stream EOF.
//
// Caller does not need to do their own terminal cleanup — Run restores
// state on every return path.
func Run(stream Stream) error {
	return RunOn(stream, os.Stdin, os.Stdout)
}

// RunOn is the testable form of Run. Passes stdin/stdout explicitly so
// tests can drive the proxy with pipes.
func RunOn(stream Stream, stdin *os.File, stdout io.Writer) error {
	fd := int(stdin.Fd())
	isTTY := term.IsTerminal(fd)

	var restore func()
	if isTTY {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		restore = func() { _ = term.Restore(fd, oldState) }
		defer restore()

		// Send the initial size.
		if cols, rows, err := term.GetSize(fd); err == nil {
			_ = stream.Resize(uint16(cols), uint16(rows))
		}
	}

	done := make(chan struct{})
	defer close(done)

	// SIGWINCH forwarder. Only meaningful for ttys.
	if isTTY {
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		go func() {
			defer signal.Stop(winch)
			for {
				select {
				case <-winch:
					if cols, rows, err := term.GetSize(fd); err == nil {
						_ = stream.Resize(uint16(cols), uint16(rows))
					}
				case <-done:
					return
				}
			}
		}()
	}

	// stdin -> stream (with detach state machine).
	detachCh := make(chan struct{})
	stdinErrCh := make(chan error, 1)
	go func() {
		state := sNormal
		buf := make([]byte, 1024)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				out := make([]byte, 0, n)
				detached := false
				for i := 0; i < n; i++ {
					var fwd []byte
					var det bool
					state, fwd, det = processInputByte(state, buf[i])
					if det {
						detached = true
						break
					}
					out = append(out, fwd...)
				}
				if len(out) > 0 {
					if _, werr := stream.Write(out); werr != nil {
						stdinErrCh <- werr
						return
					}
				}
				if detached {
					close(detachCh)
					return
				}
			}
			if err != nil {
				if pending := flushPendingPrefix(state); pending != nil {
					_, _ = stream.Write(pending)
				}
				stdinErrCh <- err
				return
			}
		}
	}()

	// stream -> stdout.
	streamErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdout, stream)
		streamErrCh <- err
	}()

	select {
	case <-detachCh:
		// User asked to detach. Tell the daemon, then return.
		_ = stream.Close()
		return nil
	case err := <-streamErrCh:
		// Daemon closed (job exit or daemon down). Stop forwarding.
		_ = stream.Close()
		if errors.Is(err, io.EOF) || err == nil {
			return nil
		}
		return err
	case err := <-stdinErrCh:
		// Local stdin closed (rare in TTY mode). Treat EOF as clean.
		_ = stream.Close()
		if errors.Is(err, io.EOF) || err == nil {
			return nil
		}
		return err
	}
}
