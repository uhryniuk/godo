package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/autospawn"
	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/proto"
	"github.com/uhryniuk/godo/internal/tui"
)

var monitCmd = &cobra.Command{
	Use:     "monit",
	Aliases: []string{"top"},
	Short:   "Live TUI dashboard of all jobs",
	Long: `monit opens a Bubble Tea dashboard that polls the daemon every second,
with hotkeys for restart, kill, and attach. Press '?' for in-app help.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runMonit()
	},
}

func runMonit() {
	config.InitConfig()
	sock := config.GetSocketPath()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := autospawn.EnsureRunning(ctx, sock, autospawn.SpawnSupervisor); err != nil {
		fmt.Fprintln(os.Stderr, "godo:", err)
		os.Exit(1)
	}
	model := tui.New(proto.NewClient(sock))
	prog := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "godo:", err)
		os.Exit(1)
	}
}
