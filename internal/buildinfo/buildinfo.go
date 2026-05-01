// Package buildinfo exposes the binary's build identity (the short git
// revision it was compiled from). Both the CLI and the daemon use it to
// detect when their builds drift, which is the trigger for the upgrade
// flow.
package buildinfo

import (
	"runtime/debug"
	"strings"
	"sync"
)

const Unknown = "unknown"

var (
	once   sync.Once
	cached string
)

// Short returns the binary's build identity in the most specific form
// available:
//
//   - Local build (go build / go install ./...): first 12 chars of
//     vcs.revision, with "+dirty" if the tree was modified.
//   - Remote install with a version tag (go install pkg@v1.2.3): the
//     module version, e.g. "v1.2.3".
//   - Remote install without a tag (go install pkg@latest): the commit
//     hash embedded in the pseudo-version, e.g. "abcdef012345".
//
// Returns "unknown" when no VCS or module metadata is present (go run,
// stripped builds, some test binaries).
//
// The dirty marker matters because two binaries from the same commit
// but with different uncommitted changes are different binaries — we
// don't want the upgrade-mismatch notice to call them equal.
func Short() string {
	once.Do(func() {
		cached = Unknown
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}

		// Prefer the embedded VCS revision — available for local builds.
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			if modified == "true" {
				rev += "+dirty"
			}
			cached = rev
			return
		}

		// Fall back to the module version set by the Go toolchain when
		// installing from a module proxy (go install pkg@version).
		// A pseudo-version (vX.Y.Z-yyyymmddhhmmss-<hash>) encodes the
		// commit hash in the last 12-char segment; a real tag is used as-is.
		if v := info.Main.Version; v != "" && v != "(devel)" {
			parts := strings.Split(v, "-")
			if len(parts) == 3 {
				// pseudo-version: last segment is the commit hash
				cached = parts[2]
			} else {
				cached = v
			}
		}
	})
	return cached
}
