package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
)

var (
	runName       string
	runWorkingDir string
	runEnv        []string // KEY=VAL repeats
	runNice       int
	runIOnice     string
	runRestart    string
)

var runCmd = &cobra.Command{
	Use:   "run [flags] -- <command> [args...]",
	Short: "Launch a job with explicit flags (--name, --restart, --nice, etc.)",
	Long: `run is the explicit form of 'godo <cmd>'. Use it when you need flags that
the bare form can't accept (which uses DisableFlagParsing so flags pass through
to the child).

Use '--' to separate godo flags from the child command:
    godo run --name web --restart on-failure -- node server.js --port 8080`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		envMap := make(map[string]string, len(runEnv))
		for _, kv := range runEnv {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				fmt.Fprintf(os.Stderr, "godo: --env entry %q must be KEY=VALUE\n", kv)
				os.Exit(1)
			}
			envMap[k] = v
		}

		req := proto.RunRequest{
			Command:    args[0],
			Args:       args[1:],
			Name:       runName,
			WorkingDir: runWorkingDir,
			Env:        envMap,
			Nice:       runNice,
			Restart:    runRestart,
		}
		resp, err := proto.NewClient(sock).Run(ctx, req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  pid=%d  %s\n", resp.Job.ShortID(), resp.Job.PID, resp.Job.Name)
	},
}

func init() {
	runCmd.Flags().StringVar(&runName, "name", "", "Custom name for the job (default: command line)")
	runCmd.Flags().StringVar(&runWorkingDir, "working-dir", "", "Working directory for the child")
	runCmd.Flags().StringSliceVar(&runEnv, "env", nil, "KEY=VALUE env entry; repeat for multiple")
	runCmd.Flags().IntVar(&runNice, "nice", 0, "POSIX nice value (-20..19)")
	runCmd.Flags().StringVar(&runIOnice, "ionice", "", "I/O priority (parsed but currently a no-op)")
	runCmd.Flags().StringVar(&runRestart, "restart", "", "Restart policy: no | on-failure | always")
}
