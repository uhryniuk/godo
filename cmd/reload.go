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

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Rescan ~/.godo/services and apply changes (added autostart, removed stop)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		resp, err := proto.NewClient(sock).ReloadServices(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		if len(resp.Added) == 0 && len(resp.Removed) == 0 && len(resp.Modified) == 0 {
			fmt.Println("(no changes)")
		}
		for _, s := range resp.Added {
			fmt.Printf("+ %s\n", s.Name)
		}
		for _, p := range resp.Removed {
			fmt.Printf("- %s\n", p)
		}
		for _, s := range resp.Modified {
			fmt.Printf("~ %s (run `godo restart %s` to apply)\n", s.Name, s.Name)
		}
		for _, e := range resp.Errors {
			fmt.Fprintln(os.Stderr, "godo:", e)
		}
		if len(resp.Errors) > 0 {
			os.Exit(1)
		}
	},
}
