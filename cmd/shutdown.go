package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
)

var shutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Stop the supervisor daemon (and every job it owns)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()

		// Don't auto-spawn — if the daemon isn't running there's nothing
		// to shut down.
		client := proto.NewClient(sock)
		if !client.Reachable(time.Second) {
			fmt.Println("(daemon not running)")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Shutdown(ctx); err != nil {
			// EOF / connection close is expected because the daemon
			// closes the conn as part of shutting down. Anything else
			// is real.
			if errors.Is(err, io.EOF) {
				return
			}
			var nerr *net.OpError
			if errors.As(err, &nerr) {
				return
			}
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Println("daemon shutting down")
	},
}
