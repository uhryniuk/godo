// Package ptyproxy is the client-side PTY proxy used by `godo attach`
// (and, later, the in-pane attach inside the TUI). It puts stdin in raw
// mode, forwards PTY output to stdout, sends SIGWINCH events as resize
// frames, and watches for the Ctrl-B d detach sequence.
package ptyproxy

// Detach key sequence borrowed from tmux: Ctrl-B then d. Two-byte
// sequence so single Ctrl-B keystrokes (which some apps use as a
// command prefix) don't accidentally detach.
const (
	detachKey1 byte = 0x02 // Ctrl-B
	detachKey2 byte = 'd'
)

// detachState is the state of the input-byte interpreter.
type detachState int

const (
	sNormal detachState = iota
	sSawPrefix
)

// processInputByte feeds one input byte through the detach state
// machine. Returns the next state, the bytes (if any) to forward to
// the daemon, and a detach signal. When detach is true, the caller
// must stop forwarding immediately.
//
// Pure function — no IO, no globals. The F14 unit test target.
func processInputByte(state detachState, b byte) (next detachState, forward []byte, detach bool) {
	switch state {
	case sNormal:
		if b == detachKey1 {
			return sSawPrefix, nil, false
		}
		return sNormal, []byte{b}, false
	case sSawPrefix:
		if b == detachKey2 {
			return sNormal, nil, true
		}
		// Not the detach completion: forward both bytes (the prefix
		// AND this byte) so legitimate keystrokes the user typed are
		// not lost.
		return sNormal, []byte{detachKey1, b}, false
	}
	return state, nil, false
}

// flushPendingPrefix returns the byte to forward when the input stream
// ends (EOF / read error) while we were holding a detach prefix.
// Without this, a trailing Ctrl-B would silently disappear.
func flushPendingPrefix(state detachState) []byte {
	if state == sSawPrefix {
		return []byte{detachKey1}
	}
	return nil
}
