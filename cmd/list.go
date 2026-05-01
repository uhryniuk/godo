package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
)

var listOutput string

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ps"},
	Short:   "List all jobs",
	Run: func(cmd *cobra.Command, args []string) {
		switch listOutput {
		case "table", "json":
		default:
			fmt.Fprintf(os.Stderr, "godo: --output: unknown format %q (want table|json)\n", listOutput)
			os.Exit(1)
		}

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

		switch listOutput {
		case "json":
			renderListJSON(resp.Jobs)
		default:
			renderListTable(resp.Jobs)
		}
	},
}

func init() {
	listCmd.Flags().StringVarP(&listOutput, "output", "o", "table", "output format: table|json")
}

func renderListTable(jobs []job.Job) {
	if len(jobs) == 0 {
		fmt.Println("(no jobs)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tPID\tUPTIME\tEXIT")
	now := time.Now()
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
			j.ShortID(), j.Name, j.State, j.PID,
			uptime(j.StartedAt, j.ExitedAt, now),
			exitStr(j.State, j.ExitCode),
		)
	}
	_ = tw.Flush()
}

// renderListJSON emits the full job records as a JSON array. Empty
// becomes []. Always emits an array — scripts can `jq length` etc.
// without special-casing the no-jobs response.
func renderListJSON(jobs []job.Job) {
	if jobs == nil {
		jobs = []job.Job{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(jobs); err != nil {
		fmt.Fprintln(os.Stderr, "godo:", err)
		os.Exit(1)
	}
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
