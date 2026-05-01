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
	"github.com/uhryniuk/godo/internal/ptyproxy"
)

var attachCmd = &cobra.Command{
	Use:   "attach <id|name>",
	Short: "Attach your terminal to a running job's PTY",
	Long: `attach proxies your local terminal into a running job's PTY. You see what
the job is printing and your keystrokes go to its stdin. Detach with Ctrl+B
followed by 'd' (the job keeps running).

Useful for talking to interactive subprocesses (REPLs, vim, htop) you launched
via 'godo run'. Window resizes are forwarded.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()

		ensureCtx, ensureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ensureCancel()
		if err := autospawn.EnsureRunning(ensureCtx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		// Attach itself is unbounded — exits on detach or job end.
		stream, err := proto.NewClient(sock).Attach(context.Background(), args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		if err := ptyproxy.Run(stream); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
	},
}
