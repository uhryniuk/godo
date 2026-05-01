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

var pauseCmd = &cobra.Command{
	Use:   "pause <id|name>",
	Short: "Freeze a running job (SIGSTOP)",
	Long: `pause sends SIGSTOP to the job's process group. The process is frozen at
the kernel level — no CPU, no progress — but its memory, file descriptors,
and listening sockets remain held. Resume with 'godo resume'.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		resp, err := proto.NewClient(sock).Pause(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  %s  %s\n", resp.Job.ShortID(), resp.Job.State, resp.Job.Name)
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume <id|name>",
	Short: "Continue a paused job (SIGCONT)",
	Long:  `resume sends SIGCONT to a previously paused job's process group.`,
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
		resp, err := proto.NewClient(sock).Resume(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  %s  %s\n", resp.Job.ShortID(), resp.Job.State, resp.Job.Name)
	},
}
