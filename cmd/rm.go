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

var rmCmd = &cobra.Command{
	Use:   "rm <id|name>",
	Short: "Remove a stopped job and its log directory",
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
		resp, err := proto.NewClient(sock).Remove(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Println("removed", resp.ID[:8])
	},
}
