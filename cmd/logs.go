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

var logsCmd = &cobra.Command{
	Use:   "logs <id|name>",
	Short: "Print a job's stdout and stderr",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		resp, err := proto.NewClient(sock).Logs(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		if resp.Stdout != "" {
			fmt.Print(resp.Stdout)
		}
		if resp.Stderr != "" {
			fmt.Fprint(os.Stderr, resp.Stderr)
		}
	},
}
