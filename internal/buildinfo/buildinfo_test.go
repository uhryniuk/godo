package buildinfo

import (
	"strings"
	"testing"
)

func TestShortReturnsSomething(t *testing.T) {
	got := Short()
	if got == "" {
		t.Fatal("Short() returned empty string")
	}
	// Either real VCS info or the explicit unknown sentinel.
	if got == Unknown {
		return
	}
	// Real revision: starts with hex chars, optional +dirty suffix.
	rev := strings.TrimSuffix(got, "+dirty")
	if len(rev) == 0 || len(rev) > 12 {
		t.Errorf("revision %q has unexpected length", rev)
	}
	for _, c := range rev {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Errorf("revision %q contains non-hex char %q", rev, c)
			break
		}
	}
}

func TestShortIsCached(t *testing.T) {
	a, b := Short(), Short()
	if a != b {
		t.Errorf("Short() not stable: %q vs %q", a, b)
	}
}
