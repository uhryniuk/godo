package cmd

import "testing"

func TestQuotedCommandHint(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantHint bool
	}{
		{"plain binary", "python3", false},
		{"relative path", "./script.sh", false},
		{"absolute path", "/usr/bin/echo", false},
		{"home path", "~/bin/tool", false},
		{"absolute path with spaces (mac app)", "/Applications/My App.app/Contents/MacOS/bin", false},
		{"quoted command", "python3 -m http.server", true},
		{"quoted command with tab", "echo\thello", true},
		{"single word with trailing space", "python3 ", true},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := quotedCommandHint(c.in)
			if (got != "") != c.wantHint {
				t.Errorf("quotedCommandHint(%q) = %q; wantHint=%v", c.in, got, c.wantHint)
			}
		})
	}
}
