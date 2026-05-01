// Package buildinfo exposes the binary's build identity (the short git
// revision it was compiled from). Both the CLI and the daemon use it to
// detect when their builds drift, which is the trigger for the upgrade
// flow.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

const Unknown = "unknown"

var (
	once   sync.Once
	cached string
)

// Short returns the first 12 chars of the binary's vcs.revision build
// setting, with "+dirty" appended if the working tree was modified at
// build time. Returns "unknown" if the binary wasn't compiled with VCS
// metadata (which happens for `go run`, some test binaries, and
// stripped builds). Cached after first call.
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
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev == "" {
			return
		}
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if modified == "true" {
			rev += "+dirty"
		}
		cached = rev
	})
	return cached
}
