package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/daemon"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the godo supervisor in the foreground",
	Long:  "Foreground daemon for development and debugging. Auto-spawn (Step 2) will use the hidden 'supervisor' subcommand instead.",
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		d := daemon.New(config.GetSocketPath())
		if err := d.Run(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}
