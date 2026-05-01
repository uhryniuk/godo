package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/buildinfo"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
)

var (
	upgradeYes   bool
	upgradeForce bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Stop the running supervisor so the next 'godo' command spawns the new binary",
	Long: `upgrade is the binary-swap flow. The running supervisor process is the
old binary until it dies, so any new CLI features won't take effect until
it's restarted. This command lists currently running jobs (which will be
killed), confirms with you, then issues a clean shutdown. The next 'godo'
invocation auto-spawns the new binary.

If the running daemon's build matches the CLI's, upgrade reports nothing
to do and exits — pass --force to shut it down anyway.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()

		client := proto.NewClient(sock)
		if !client.Reachable(time.Second) {
			fmt.Println("(daemon not running; nothing to upgrade)")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		ping, err := client.Ping(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo: ping daemon:", err)
			os.Exit(1)
		}
		cliBuild := buildinfo.Short()
		daemonBuild := ping.BuildVersion
		if daemonBuild == "" {
			daemonBuild = buildinfo.Unknown
		}

		if !upgradeForce && cliBuild != buildinfo.Unknown && daemonBuild == cliBuild {
			fmt.Printf("supervisor already at %s; nothing to do (use --force to restart anyway)\n", cliBuild)
			return
		}

		// Surface what's about to die.
		listResp, err := client.List(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo: list jobs:", err)
			os.Exit(1)
		}
		var running []job.Job
		for _, j := range listResp.Jobs {
			if j.State == job.Running {
				running = append(running, j)
			}
		}

		fmt.Printf("supervisor build: %s\n", daemonBuild)
		fmt.Printf("CLI build:        %s\n", cliBuild)
		if len(running) == 0 {
			fmt.Println("running jobs:     (none)")
		} else {
			fmt.Printf("running jobs:     %d will be terminated\n", len(running))
			for _, j := range running {
				fmt.Printf("  - %s  %s  pid=%d\n", j.ShortID(), j.Name, j.PID)
			}
		}

		if !upgradeYes {
			if !confirm("proceed with shutdown? [y/N] ") {
				fmt.Println("aborted")
				return
			}
		}

		if err := client.Shutdown(ctx); err != nil {
			// EOF / closed-conn is the daemon hanging up while shutting
			// down — expected, not a failure. Anything else is real.
			if !errors.Is(err, io.EOF) {
				var nerr *net.OpError
				if !errors.As(err, &nerr) {
					fmt.Fprintln(os.Stderr, "godo: shutdown:", err)
					os.Exit(1)
				}
			}
		}

		// Confirm the daemon is actually gone before reporting success.
		exitDeadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(exitDeadline) {
			if !client.Reachable(200 * time.Millisecond) {
				fmt.Printf("supervisor stopped; the next 'godo' command will start %s\n", cliBuild)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, "godo: daemon still reachable after 5s — shutdown may be hung")
		os.Exit(1)
	},
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeYes, "yes", false, "skip the interactive confirmation")
	upgradeCmd.Flags().BoolVar(&upgradeForce, "force", false, "shut down even if CLI and daemon report the same build")
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}
