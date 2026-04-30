package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs <id|name>",
	Short: "Print or follow a job's combined output",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()

		// Short timeout for one-shot, unbounded for follow.
		ctx := context.Background()
		if !logsFollow {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
		}

		ensureCtx, ensureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ensureCancel()
		if err := autospawn.EnsureRunning(ensureCtx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		client := proto.NewClient(sock)

		if logsFollow {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(sigCh)
			go func() {
				<-sigCh
				cancel()
			}()

			err := client.LogsFollow(ctx, args[0], func(chunk []byte) error {
				_, werr := os.Stdout.Write(chunk)
				return werr
			})
			if err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "godo:", err)
				os.Exit(1)
			}
			return
		}

		resp, err := client.Logs(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Print(resp.Output)
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Stream log updates as they happen (Ctrl+C to stop)")
}
