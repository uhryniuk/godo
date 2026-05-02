package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/buildinfo"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
)

// versionNoticeSkip lists commands whose PreRun should not print the
// supervisor build-mismatch notice. Daemon-management commands all skip
// (the notice would be noise: shutdown is going to bring it down anyway,
// version/upgrade explicitly surface build info, supervisor IS the daemon).
var versionNoticeSkip = map[string]bool{
	"supervisor": true,
	"daemon":     true,
	"version":    true,
	"upgrade":    true,
	"shutdown":   true,
}

// warnIfBuildMismatch pings the daemon (without auto-spawning) and prints
// a one-line dim notice on stderr if its build differs from the CLI's.
// Silent on every other condition: daemon unreachable, either side
// reporting "unknown", or matching builds. Always best-effort — never
// blocks or fails the surrounding command.
func warnIfBuildMismatch(cmdName string) {
	if versionNoticeSkip[cmdName] {
		return
	}
	cliBuild := buildinfo.Short()
	if cliBuild == buildinfo.Unknown {
		return
	}
	sock := config.GetSocketPath()
	client := proto.NewClient(sock)
	if !client.Reachable(200 * time.Millisecond) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	ping, err := client.Ping(ctx)
	if err != nil || ping.BuildVersion == "" || ping.BuildVersion == buildinfo.Unknown {
		return
	}
	if ping.BuildVersion == cliBuild {
		return
	}
	const dim, reset = "\x1b[2m", "\x1b[0m"
	fmt.Fprintf(os.Stderr,
		"%sgodo: supervisor running build %s, CLI is %s — run 'godo upgrade' to refresh%s\n",
		dim, ping.BuildVersion, cliBuild, reset)
}

var rootCmd = &cobra.Command{
	Use:   "godo [--name <label>] <command> [args...]",
	Short: "Run and manage long-lived background processes",
	Long: `godo runs commands as supervised background processes that survive shell
logout. Equivalent to 'godo run' — passes its positional args to the daemon
which spawns and tracks the process.

Pass --name (or -n) before the command to label the job; afterwards
'godo stop|restart|rm <label>' resolves to it instead of the hash:

    godo --name web sh -c "python3 -m http.server 8888"
    godo rm web`,
	DisableFlagParsing: true, // pass everything after 'godo' through to the child
	Args:               cobra.MinimumNArgs(0),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		warnIfBuildMismatch(cmd.Name())
	},
	Run: func(cmd *cobra.Command, args []string) {
		// DisableFlagParsing means the leading --help / -h doesn't get
		// special-cased by cobra. Catch it here so `godo --help` shows
		// help instead of being treated as a child command name.
		if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
			_ = cmd.Help()
			return
		}
		// `godo -i` is the shortcut to launch the TUI dashboard.
		if args[0] == "-i" || args[0] == "--interactive" {
			runMonit()
			return
		}

		name, args, err := consumeLeadingName(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "godo: --name requires a command after it")
			os.Exit(1)
		}

		// Catch the "godo 'python3 -m http.server'" mistake before we
		// spin up the daemon and chase a doomed exec.
		if hint := quotedCommandHint(args[0]); hint != "" {
			fmt.Fprintln(os.Stderr, hint)
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

		absCmd, err := exec.LookPath(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		req := proto.RunRequest{
			Command: absCmd,
			Args:    args[1:],
			Name:    name,
		}
		client := proto.NewClient(sock)
		resp, err := client.Run(ctx, req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  pid=%d  %s\n", resp.Job.ShortID(), resp.Job.PID, resp.Job.Name)
		if code := reportIfFastFail(ctx, client, resp.Job.Hash, os.Stderr); code != 0 {
			os.Exit(code)
		}
	},
}

// consumeLeadingName strips an optional --name/-n flag (with either a
// space-separated or =-joined value) from the head of args. Only the
// FIRST token is considered, so 'godo sh -c "echo --name x"' still
// passes --name through to the child untouched. Returns the parsed
// name (or "") and the remaining args.
func consumeLeadingName(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", args, nil
	}
	head := args[0]
	switch {
	case head == "--name" || head == "-n":
		if len(args) < 2 {
			return "", nil, fmt.Errorf("%s requires a value", head)
		}
		return args[1], args[2:], nil
	case strings.HasPrefix(head, "--name="):
		v := strings.TrimPrefix(head, "--name=")
		if v == "" {
			return "", nil, fmt.Errorf("--name= requires a value")
		}
		return v, args[1:], nil
	case strings.HasPrefix(head, "-n="):
		v := strings.TrimPrefix(head, "-n=")
		if v == "" {
			return "", nil, fmt.Errorf("-n= requires a value")
		}
		return v, args[1:], nil
	}
	return "", args, nil
}

func Execute() {
	rootCmd.AddCommand(
		supervisorCmd,
		daemonCmd,
		listCmd,
		stopCmd,
		restartCmd,
		pauseCmd,
		resumeCmd,
		rmCmd,
		logsCmd,
		attachCmd,
		loadCmd,
		reloadCmd,
		startCmd,
		templateCmd,
		servicesCmd,
		monitCmd,
		runCmd,
		shutdownCmd,
		upgradeCmd,
		versionCmd,
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
