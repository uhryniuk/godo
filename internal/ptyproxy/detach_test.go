package ptyproxy

import (
	"bytes"
	"testing"
)

func TestProcessInputByteTable(t *testing.T) {
	cases := []struct {
		name        string
		state       detachState
		b           byte
		wantState   detachState
		wantForward []byte
		wantDetach  bool
	}{
		{"normal byte from normal", sNormal, 'a', sNormal, []byte{'a'}, false},
		{"prefix byte from normal", sNormal, detachKey1, sSawPrefix, nil, false},
		{"detach completes", sSawPrefix, detachKey2, sNormal, nil, true},
		{"prefix then non-d forwards both", sSawPrefix, 'x', sNormal, []byte{detachKey1, 'x'}, false},
		{"prefix then prefix forwards both", sSawPrefix, detachKey1, sNormal, []byte{detachKey1, detachKey1}, false},
		{"newline normal", sNormal, '\n', sNormal, []byte{'\n'}, false},
		{"null byte normal", sNormal, 0x00, sNormal, []byte{0x00}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotState, gotForward, gotDetach := processInputByte(c.state, c.b)
			if gotState != c.wantState {
				t.Errorf("state: got %d, want %d", gotState, c.wantState)
			}
			if !bytes.Equal(gotForward, c.wantForward) {
				t.Errorf("forward: got %v, want %v", gotForward, c.wantForward)
			}
			if gotDetach != c.wantDetach {
				t.Errorf("detach: got %v, want %v", gotDetach, c.wantDetach)
			}
		})
	}
}

func TestFlushPendingPrefix(t *testing.T) {
	if got := flushPendingPrefix(sNormal); got != nil {
		t.Errorf("normal state: got %v, want nil", got)
	}
	if got := flushPendingPrefix(sSawPrefix); !bytes.Equal(got, []byte{detachKey1}) {
		t.Errorf("prefix state: got %v, want %v", got, []byte{detachKey1})
	}
}

// processStream walks a byte sequence through the state machine and
// returns the cumulative forwarded bytes plus whether a detach fired.
// Used as a higher-level smoke that exercises typical input scenarios.
func processStream(in []byte) (forwarded []byte, detached bool) {
	state := sNormal
	for _, b := range in {
		var fwd []byte
		var det bool
		state, fwd, det = processInputByte(state, b)
		if det {
			return forwarded, true
		}
		forwarded = append(forwarded, fwd...)
	}
	if pending := flushPendingPrefix(state); pending != nil {
		forwarded = append(forwarded, pending...)
	}
	return forwarded, false
}

func TestProcessStreamScenarios(t *testing.T) {
	cases := []struct {
		name          string
		in            []byte
		wantForwarded []byte
		wantDetached  bool
	}{
		{"plain typing", []byte("hello\n"), []byte("hello\n"), false},
		{"detach mid-stream", append(append([]byte("hi"), detachKey1, detachKey2), []byte("more")...), []byte("hi"), true},
		{"prefix then x then more", []byte{'a', detachKey1, 'x', 'b'}, []byte{'a', detachKey1, 'x', 'b'}, false},
		{"trailing prefix gets flushed on EOF", []byte{'q', detachKey1}, []byte{'q', detachKey1}, false},
		{"empty input", []byte{}, nil, false},
		{"detach at very start", []byte{detachKey1, detachKey2, 'x'}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFwd, gotDet := processStream(c.in)
			if gotDet != c.wantDetached {
				t.Errorf("detached: got %v, want %v", gotDet, c.wantDetached)
			}
			if !bytes.Equal(gotFwd, c.wantForwarded) {
				t.Errorf("forwarded: got %v, want %v", gotFwd, c.wantForwarded)
			}
		})
	}
}
