package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/daemon"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print godo version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// daemon.Version is the wire/behaviour version the daemon
		// reports over Ping. The build version comes from go's debug
		// info (set when built with VCS metadata).
		buildInfo := "unknown"
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" {
					buildInfo = s.Value
					if len(buildInfo) > 12 {
						buildInfo = buildInfo[:12]
					}
				}
			}
		}
		fmt.Printf("godo %s (build %s)\n", daemon.Version, buildInfo)
	},
}
