package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/buildinfo"
	"github.com/uhryniuk/godo/internal/daemon"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print godo version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// daemon.Version is the wire/behaviour version. buildinfo.Short
		// is the git revision the binary was built from.
		fmt.Printf("godo %s (build %s)\n", daemon.Version, buildinfo.Short())
	},
}
