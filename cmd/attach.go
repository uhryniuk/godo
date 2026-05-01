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
	"github.com/uhryniuk/godo/internal/ptyproxy"
)

var attachCmd = &cobra.Command{
	Use:   "attach <id|name>",
	Short: "Attach your terminal to a running job's PTY",
	Long: `attach proxies your local terminal into a running job's PTY. You see what
the job is printing and your keystrokes go to its stdin. Default detach
shortcut is Ctrl+X then d. Override via the GODO_DETACH environment
variable, e.g. GODO_DETACH="Ctrl+B,d".

Useful for talking to interactive subprocesses (REPLs, vim, htop) you launched
via 'godo run'. Window resizes are forwarded.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		sock := config.GetSocketPath()

		ensureCtx, ensureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ensureCancel()
		if err := autospawn.EnsureRunning(ensureCtx, sock, autospawn.SpawnSupervisor); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		seq, opts := resolveDetachOpts()

		client := proto.NewClient(sock)

		// Replay existing log before going live so the user sees what
		// happened before they attached. Use a short-lived context for
		// the one-shot RPC; the attach stream itself is unbounded.
		replayCtx, replayCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if logs, err := client.Logs(replayCtx, args[0]); err == nil && logs.Output != "" {
			fmt.Print(logs.Output)
		}
		replayCancel()

		// Attach itself is unbounded — exits on detach or job end.
		stream, err := client.Attach(context.Background(), args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Println(ptyproxy.Banner(args[0], seq))
		if err := ptyproxy.Run(stream, opts...); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
	},
}

// resolveDetachOpts reads $GODO_DETACH (e.g. "Ctrl+B,d") and returns
// the parsed sequence + the ptyproxy options that should be passed to
// Run. On parse error, falls back to the default with a stderr warning.
func resolveDetachOpts() ([]byte, []ptyproxy.Option) {
	raw := os.Getenv("GODO_DETACH")
	if raw == "" {
		return ptyproxy.DefaultDetachSequence, nil
	}
	seq, err := ptyproxy.ParseDetachSequence(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "godo: GODO_DETACH=%q: %v; falling back to default\n", raw, err)
		return ptyproxy.DefaultDetachSequence, nil
	}
	return seq, []ptyproxy.Option{ptyproxy.WithDetachSequence(seq)}
}
