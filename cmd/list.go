package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ps"},
	Short:   "List all jobs",
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		resp, err := proto.NewClient(sock).List(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		if len(resp.Jobs) == 0 {
			fmt.Println("(no jobs)")
			return
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tSTATE\tPID\tUPTIME\tEXIT")
		now := time.Now()
		for _, j := range resp.Jobs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
				j.ShortID(), j.Name, j.State, j.PID,
				uptime(j.StartedAt, j.ExitedAt, now),
				exitStr(j.State, j.ExitCode),
			)
		}
		_ = tw.Flush()
	},
}

func uptime(started, exited, now time.Time) string {
	if started.IsZero() {
		return "-"
	}
	end := now
	if !exited.IsZero() {
		end = exited
	}
	return end.Sub(started).Round(time.Second).String()
}

func exitStr(state any, code int) string {
	s := fmt.Sprint(state)
	if s == "running" || s == "pending" {
		return "-"
	}
	return fmt.Sprintf("%d", code)
}
