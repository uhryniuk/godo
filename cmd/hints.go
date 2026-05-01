package cmd

import (
	"fmt"
	"strings"
)

// quotedCommandHint detects the common "godo 'python3 -m http.server'"
// invocation mistake — where the user quoted their whole command and
// got it collapsed into a single argv slot. Returns a one-line hint
// suggesting the correct form, or empty if arg looks normal.
//
// We allow paths with spaces (e.g. macOS app bundles like
// /Applications/My App.app/...) by skipping the check when the first
// character is /, ., or ~.
func quotedCommandHint(arg string) string {
	if !strings.ContainsAny(arg, " \t") {
		return ""
	}
	if len(arg) > 0 {
		switch arg[0] {
		case '/', '.', '~':
			return ""
		}
	}
	return fmt.Sprintf(
		`godo: %q looks like a quoted shell command — drop the quotes, or use 'godo sh -c "..."' if you need shell features`,
		arg,
	)
}
