// Package tui is the godo monit dashboard. It runs the Bubble Tea
// program that polls the daemon for the job list and renders a sortable
// table, with hotkeys for restart / kill / attach.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
	"github.com/uhryniuk/godo/internal/ptyproxy"
)

const refreshInterval = 1 * time.Second

// Model is the top-level Bubble Tea model.
type Model struct {
	client *proto.Client

	jobs     []job.Job
	selected int
	width    int
	height   int

	statusMsg string // transient one-line message, cleared on next refresh
	loadErr   error
}

// New constructs the model. It does NOT do any I/O — that happens in
// Init / Update via tea.Cmds.
func New(client *proto.Client) Model {
	return Model{client: client}
}

// Init kicks off the first list fetch and the recurring tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchJobs(), tickCmd())
}

// tickMsg is delivered every refreshInterval to trigger a refresh.
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// jobsLoadedMsg is the result of a List RPC.
type jobsLoadedMsg struct {
	jobs []job.Job
	err  error
}

// fetchJobs returns a tea.Cmd that calls the daemon's List RPC.
func (m Model) fetchJobs() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := client.List(ctx)
		if err != nil {
			return jobsLoadedMsg{err: err}
		}
		return jobsLoadedMsg{jobs: resp.Jobs}
	}
}

// statusClearedMsg blanks the transient status line after a delay.
type statusClearedMsg struct{}

func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return statusClearedMsg{} })
}

// attachExitedMsg comes back from tea.ExecProcess after the user
// detaches from the PTY proxy.
type attachExitedMsg struct{ err error }

// attachExec implements tea.ExecCommand to run ptyproxy.Run in
// suspended-Bubble-Tea mode.
type attachExec struct {
	client *proto.Client
	target string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (e *attachExec) SetStdin(r io.Reader)  { e.stdin = r }
func (e *attachExec) SetStdout(w io.Writer) { e.stdout = w }
func (e *attachExec) SetStderr(w io.Writer) { e.stderr = w }

func (e *attachExec) Run() error {
	stream, err := e.client.Attach(context.Background(), e.target)
	if err != nil {
		fmt.Fprintln(e.stderr, "attach:", err)
		return err
	}
	return ptyproxy.Run(stream)
}

// Update is the Bubble Tea reducer.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetchJobs(), tickCmd())

	case jobsLoadedMsg:
		if msg.err != nil {
			m.loadErr = msg.err
			return m, nil
		}
		m.loadErr = nil
		m.jobs = msg.jobs
		// Clamp selection if the list shrank.
		if m.selected >= len(m.jobs) {
			m.selected = len(m.jobs) - 1
		}
		if m.selected < 0 {
			m.selected = 0
		}
		return m, nil

	case statusClearedMsg:
		m.statusMsg = ""
		return m, nil

	case attachExitedMsg:
		if msg.err != nil {
			m.statusMsg = "attach: " + msg.err.Error()
		} else {
			m.statusMsg = "detached"
		}
		return m, clearStatusAfter(2 * time.Second)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		m.selected = clamp(m.selected+1, 0, len(m.jobs)-1)
	case "k", "up":
		m.selected = clamp(m.selected-1, 0, len(m.jobs)-1)
	case "g", "home":
		m.selected = 0
	case "G", "end":
		m.selected = clamp(len(m.jobs)-1, 0, len(m.jobs)-1)

	case "r":
		if j, ok := m.currentJob(); ok {
			return m, m.restart(j.Hash)
		}
	case "K":
		if j, ok := m.currentJob(); ok {
			return m, m.stop(j.Hash)
		}
	case "enter":
		if j, ok := m.currentJob(); ok {
			if j.State != job.Running {
				m.statusMsg = "can only attach to a running job"
				return m, clearStatusAfter(2 * time.Second)
			}
			return m, tea.Exec(
				&attachExec{client: m.client, target: j.Hash},
				func(err error) tea.Msg { return attachExitedMsg{err: err} },
			)
		}

	case "?":
		m.statusMsg = "j/k=move  r=restart  K=kill  enter=attach  q=quit"
		return m, clearStatusAfter(4 * time.Second)
	}

	return m, nil
}

func (m Model) currentJob() (job.Job, bool) {
	if m.selected < 0 || m.selected >= len(m.jobs) {
		return job.Job{}, false
	}
	return m.jobs[m.selected], true
}

func (m Model) restart(hash string) tea.Cmd {
	client := m.client
	return tea.Batch(
		m.flashStatus("restarting…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Restart(ctx, hash); err != nil {
				return jobsLoadedMsg{err: err}
			}
			return nil
		},
		m.fetchJobs(),
	)
}

func (m Model) stop(hash string) tea.Cmd {
	client := m.client
	return tea.Batch(
		m.flashStatus("stopping…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Stop(ctx, hash); err != nil {
				return jobsLoadedMsg{err: err}
			}
			return nil
		},
		m.fetchJobs(),
	)
}

func (m Model) flashStatus(s string) tea.Cmd {
	return func() tea.Msg { return statusFlashMsg(s) }
}

type statusFlashMsg string

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderRow formats one job into a single line. Public-ish for tests.
func renderRow(j job.Job, now time.Time, selected bool) string {
	uptime := "-"
	if !j.StartedAt.IsZero() {
		end := now
		if !j.ExitedAt.IsZero() {
			end = j.ExitedAt
		}
		uptime = end.Sub(j.StartedAt).Round(time.Second).String()
	}
	exit := "-"
	if j.State == job.Completed || j.State == job.Failed || j.State == job.Cancelled {
		exit = fmt.Sprintf("%d", j.ExitCode)
	}
	prefix := "  "
	if selected {
		prefix = "> "
	}
	return fmt.Sprintf("%s%-8s  %-32s  %-10s  %-7d  %-8s  %s",
		prefix, j.ShortID(), truncName(j.Name, 32), j.State, j.PID, uptime, exit)
}

func truncName(name string, max int) string {
	runes := []rune(name)
	if len(runes) <= max {
		return name
	}
	return string(runes[:max-1]) + "…"
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	statusStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)

// View is the Bubble Tea renderer.
func (m Model) View() string {
	var b strings.Builder

	header := fmt.Sprintf("  %-8s  %-32s  %-10s  %-7s  %-8s  %s",
		"ID", "NAME", "STATE", "PID", "UPTIME", "EXIT")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	if m.loadErr != nil {
		b.WriteString(errorStyle.Render("  daemon: " + m.loadErr.Error()))
		b.WriteString("\n")
	}

	now := time.Now()
	if len(m.jobs) == 0 && m.loadErr == nil {
		b.WriteString("  (no jobs)\n")
	}
	for i, j := range m.jobs {
		b.WriteString(renderRow(j, now, i == m.selected))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	hint := "j/k move · r restart · K kill · enter attach · ? help · q quit"
	if m.statusMsg != "" {
		hint = m.statusMsg
	}
	b.WriteString(statusStyle.Render("  " + hint))
	b.WriteString("\n")
	return b.String()
}
