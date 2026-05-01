package ptyproxy

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseDetachSequence(t *testing.T) {
	cases := []struct {
		in      string
		want    []byte
		wantErr bool
	}{
		{"Ctrl+P,Ctrl+Q", []byte{0x10, 0x11}, false},
		{"ctrl+p,ctrl+q", []byte{0x10, 0x11}, false},
		{"Ctrl+B,d", []byte{0x02, 'd'}, false},
		{"Ctrl+B, d", []byte{0x02, 'd'}, false}, // whitespace tolerated
		{"a,b,c", []byte{'a', 'b', 'c'}, false},
		{"Ctrl+A", []byte{0x01}, false},
		{"", nil, true},
		{",", nil, true},
		{"Ctrl+", nil, true},
		{"Ctrl+ab", nil, true},
		{"Ctrl+1", nil, true}, // requires a letter
		{"abc", nil, true},    // multi-char without Ctrl+
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseDetachSequence(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestFormatDetachSequence(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0x10, 0x11}, "Ctrl+P then Ctrl+Q"},
		{[]byte{0x02, 'd'}, "Ctrl+B then d"},
		{[]byte{'q'}, "q"},
		{[]byte{}, ""},
		{[]byte{0x80}, "0x80"},
	}
	for _, c := range cases {
		got := FormatDetachSequence(c.in)
		if got != c.want {
			t.Errorf("FormatDetachSequence(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseFormatRoundtrip(t *testing.T) {
	for _, s := range []string{"Ctrl+P,Ctrl+Q", "Ctrl+B,d", "q"} {
		bytes_, err := ParseDetachSequence(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		formatted := FormatDetachSequence(bytes_)
		// Roundtrip isn't byte-equal (we use " then " instead of ",")
		// but every component should appear.
		for _, part := range strings.Split(s, ",") {
			part = strings.TrimSpace(part)
			if !strings.Contains(formatted, part) {
				t.Errorf("formatted %q missing component %q", formatted, part)
			}
		}
	}
}

// matcher tests — feed bytes through a matcher and assert the
// (forward, detach) pairs.

func TestMatcherSimpleSequence(t *testing.T) {
	m := newMatcher([]byte{0x10, 0x11}) // Ctrl+P, Ctrl+Q

	// "ab" → forward both, no detach.
	if fwd, det := m.feed('a'); !bytes.Equal(fwd, []byte{'a'}) || det {
		t.Errorf("'a': fwd=%v det=%v", fwd, det)
	}
	if fwd, det := m.feed('b'); !bytes.Equal(fwd, []byte{'b'}) || det {
		t.Errorf("'b': fwd=%v det=%v", fwd, det)
	}

	// Ctrl+P → hold, no forward, no detach yet.
	if fwd, det := m.feed(0x10); fwd != nil || det {
		t.Errorf("Ctrl+P: fwd=%v det=%v", fwd, det)
	}

	// Ctrl+Q → detach.
	if fwd, det := m.feed(0x11); fwd != nil || !det {
		t.Errorf("Ctrl+Q: fwd=%v det=%v", fwd, det)
	}
}

func TestMatcherPartialMatchFlushedOnMismatch(t *testing.T) {
	m := newMatcher([]byte{0x10, 0x11})
	// Ctrl+P then 'x' → forward both bytes, no detach.
	if fwd, det := m.feed(0x10); fwd != nil || det {
		t.Fatalf("Ctrl+P should hold: fwd=%v det=%v", fwd, det)
	}
	fwd, det := m.feed('x')
	if det {
		t.Errorf("should not detach")
	}
	if !bytes.Equal(fwd, []byte{0x10, 'x'}) {
		t.Errorf("fwd: got %v, want [0x10, 'x']", fwd)
	}
}

func TestMatcherFlushReturnsHeld(t *testing.T) {
	m := newMatcher([]byte{0x10, 0x11})
	if fwd, det := m.feed(0x10); fwd != nil || det {
		t.Fatalf("Ctrl+P: fwd=%v det=%v", fwd, det)
	}
	if got := m.flush(); !bytes.Equal(got, []byte{0x10}) {
		t.Errorf("flush: got %v, want [0x10]", got)
	}
	// After flush, held should be empty.
	if got := m.flush(); got != nil {
		t.Errorf("second flush should return nil, got %v", got)
	}
}

func TestMatcherSingleCharSequence(t *testing.T) {
	m := newMatcher([]byte{'q'})
	if fwd, det := m.feed('a'); !bytes.Equal(fwd, []byte{'a'}) || det {
		t.Errorf("'a': fwd=%v det=%v", fwd, det)
	}
	if fwd, det := m.feed('q'); fwd != nil || !det {
		t.Errorf("'q': fwd=%v det=%v", fwd, det)
	}
}

func TestMatcherDefaultsWhenSeqEmpty(t *testing.T) {
	m := newMatcher(nil)
	// Should use DefaultDetachSequence (Ctrl+X, d).
	if _, det := m.feed(0x18); det {
		t.Errorf("Ctrl+X should hold under default seq")
	}
	if _, det := m.feed('d'); !det {
		t.Errorf("Ctrl+X+d should detach under default seq")
	}
}

func TestBannerIncludesTargetAndShortcut(t *testing.T) {
	got := Banner("web", DefaultDetachSequence)
	if !strings.Contains(got, "web") {
		t.Errorf("banner missing target: %q", got)
	}
	if !strings.Contains(got, "Ctrl+X then d") {
		t.Errorf("banner missing detach shortcut: %q", got)
	}
}

func TestBannerWithCustomSequence(t *testing.T) {
	got := Banner("svc", []byte{0x02, 'd'})
	if !strings.Contains(got, "Ctrl+B then d") {
		t.Errorf("banner with custom seq: %q", got)
	}
}
