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

var startCmd = &cobra.Command{
	Use:   "start <service-name>",
	Short: "Start a registered service by name",
	Long: `Start a service defined in ~/.godo/services/ by its name.

The service must already be registered (via 'godo load' or 'godo reload').
Use 'godo services' to list available services.

If the service is already running, use 'godo restart' instead.`,
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

		client := proto.NewClient(sock)
		resp, err := client.StartService(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  pid=%d  %s\n", resp.Job.ShortID(), resp.Job.PID, resp.Job.Name)
	},
}
