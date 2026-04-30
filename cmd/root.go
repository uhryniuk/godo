package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
)

var rootCmd = &cobra.Command{
	Use:   "godo [flags] <command> [args...]",
	Short: "Run and manage long-lived background processes",
	Long: `godo runs commands as supervised background processes that survive shell
logout. Equivalent to 'godo run' — passes its positional args to the daemon
which spawns and tracks the process.`,
	DisableFlagParsing: true, // pass everything after 'godo' through to the child
	Args:               cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			_ = cmd.Help()
			return
		}

		config.InitConfig()
		sock := config.GetSocketPath()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		req := proto.RunRequest{
			Command: args[0],
			Args:    args[1:],
		}
		resp, err := proto.NewClient(sock).Run(ctx, req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  pid=%d  %s\n", resp.Job.ShortID(), resp.Job.PID, resp.Job.Name)
	},
}

func Execute() {
	rootCmd.AddCommand(
		supervisorCmd,
		daemonCmd,
		listCmd,
		stopCmd,
		restartCmd,
		rmCmd,
		logsCmd,
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
