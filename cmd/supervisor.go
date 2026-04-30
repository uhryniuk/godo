package cmd

import "github.com/spf13/cobra"

// supervisorCmd is the hidden double-fork target used by autospawn. Same
// code path as `godo daemon`; the separate command exists so that
// `pgrep -f "godo supervisor"` cleanly identifies auto-spawned daemons
// while `godo daemon` remains the explicit foreground command.
var supervisorCmd = &cobra.Command{
	Use:    "supervisor",
	Hidden: true,
	Run:    daemonCmd.Run,
}
