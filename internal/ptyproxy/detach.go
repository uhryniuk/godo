// Package ptyproxy is the client-side PTY proxy used by `godo attach`
// (and the in-pane attach inside the TUI). It puts stdin in raw mode,
// forwards PTY output to stdout, sends SIGWINCH events as resize
// frames, and watches for a configurable detach key sequence.
package ptyproxy

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultDetachSequence is the default detach hotkey: Ctrl+P then
// Ctrl+Q (Docker's convention). Two bytes so the prefix Ctrl+P alone
// (which is "previous-line" in many readline modes) doesn't fire a
// detach. Doesn't collide with the prefix keys of tmux (Ctrl+B),
// screen (Ctrl+A), or zellij (Ctrl+G).
var DefaultDetachSequence = []byte{0x10, 0x11} // Ctrl+P, Ctrl+Q

// matcher is a tiny streaming sequence detector. Feed bytes one at a
// time; when the configured sequence has been observed in order, the
// next feed returns detach=true. Mismatched partial-matches are
// flushed back to the caller so a Ctrl+P followed by an unrelated
// keystroke still reaches the child.
//
// Not KMP — a partial match followed by another start of the
// sequence within the held buffer is not back-tracked. For v1 inputs
// (2-byte sequences chosen for non-overlap) this is fine.
type matcher struct {
	seq  []byte
	held []byte // bytes matched so far
}

func newMatcher(seq []byte) *matcher {
	if len(seq) == 0 {
		seq = DefaultDetachSequence
	}
	return &matcher{seq: seq}
}

// feed processes one input byte. Returns the bytes (if any) the caller
// should forward to the daemon, and detach=true on a complete match.
func (m *matcher) feed(b byte) (forward []byte, detach bool) {
	next := m.seq[len(m.held)]
	if b == next {
		m.held = append(m.held, b)
		if len(m.held) == len(m.seq) {
			m.held = m.held[:0]
			return nil, true
		}
		return nil, false
	}
	// Mismatch. Flush what we held plus this byte.
	out := make([]byte, 0, len(m.held)+1)
	out = append(out, m.held...)
	out = append(out, b)
	m.held = m.held[:0]
	return out, false
}

// flush returns any held bytes (only matters at EOF or detach when
// the proxy bails mid-sequence).
func (m *matcher) flush() []byte {
	if len(m.held) == 0 {
		return nil
	}
	out := append([]byte{}, m.held...)
	m.held = m.held[:0]
	return out
}

// ParseDetachSequence interprets a comma-separated spec (e.g.
// "Ctrl+P,Ctrl+Q" or "Ctrl+B,d") into a byte sequence. Whitespace
// around commas is ignored; the "Ctrl+" prefix is case-insensitive.
// Single ASCII characters pass through as their byte value.
func ParseDetachSequence(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty detach sequence")
	}
	parts := strings.Split(s, ",")
	out := make([]byte, 0, len(parts))
	for _, p := range parts {
		b, err := parseDetachToken(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func parseDetachToken(t string) (byte, error) {
	if t == "" {
		return 0, errors.New("empty detach token")
	}
	if len(t) >= 5 && strings.EqualFold(t[:5], "ctrl+") {
		rest := t[5:]
		if len(rest) != 1 {
			return 0, fmt.Errorf("Ctrl+ requires a single character: %q", t)
		}
		c := rest[0]
		switch {
		case c >= 'a' && c <= 'z':
			return c - 'a' + 1, nil // Ctrl+a -> 0x01
		case c >= 'A' && c <= 'Z':
			return c - 'A' + 1, nil
		default:
			return 0, fmt.Errorf("Ctrl+ requires a letter: %q", t)
		}
	}
	if len(t) == 1 {
		return t[0], nil
	}
	return 0, fmt.Errorf("unrecognized detach token %q (use Ctrl+X or a single character)", t)
}

// FormatDetachSequence renders a byte sequence as the human-readable
// "Ctrl+P Ctrl+Q" form for the banner and docs.
func FormatDetachSequence(seq []byte) string {
	if len(seq) == 0 {
		return ""
	}
	parts := make([]string, 0, len(seq))
	for _, b := range seq {
		switch {
		case b >= 0x01 && b <= 0x1A:
			parts = append(parts, fmt.Sprintf("Ctrl+%c", 'A'+b-1))
		case b >= 0x20 && b < 0x7F:
			parts = append(parts, string(b))
		default:
			parts = append(parts, fmt.Sprintf("0x%02X", b))
		}
	}
	return strings.Join(parts, " then ")
}

// Banner returns the one-line connection notice printed before raw
// mode is enabled. Both `godo attach` and the TUI's in-pane attach
// emit this so users always see the detach shortcut at the moment of
// entry.
//
// Style: dim ANSI escape so it sits visually below whatever the
// child process renders. Plain ASCII fallback if the caller's terminal
// strips escapes (the bytes are still readable).
func Banner(target string, seq []byte) string {
	const dim = "\x1b[2m"
	const reset = "\x1b[0m"
	if len(seq) == 0 {
		seq = DefaultDetachSequence
	}
	return dim + "[godo] attached to " + target + " — " + FormatDetachSequence(seq) + " to detach" + reset
}
