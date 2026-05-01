// Package tui is the godo monit dashboard. It runs the Bubble Tea
// program that polls the daemon for the job list and renders a sortable
// table, with hotkeys for restart / kill / attach.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
	"github.com/uhryniuk/godo/internal/ptyproxy"
)

const refreshInterval = 1 * time.Second

// viewMode is the current screen — the dashboard table by default,
// the log viewer when the user opens a non-running job's output.
type viewMode int

const (
	modeDashboard viewMode = iota
	modeLogs
)

// Model is the top-level Bubble Tea model.
type Model struct {
	client *proto.Client

	jobs     []job.Job
	selected int
	width    int
	height   int

	statusMsg string // transient one-line message, cleared on next refresh
	loadErr   error

	mode viewMode
	// Log viewer state — only meaningful when mode == modeLogs.
	logTitle  string
	logLines  []string
	logScroll int

	// pendingDelete holds the hash of a job awaiting y/N confirmation
	// for removal. Empty when no prompt is active. The status bar
	// renders the prompt; the next keypress resolves it.
	pendingDelete string
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

// logsLoadedMsg carries the result of a Logs RPC for the in-TUI log
// viewer. Triggered when the user presses Enter on a non-running job.
type logsLoadedMsg struct {
	title string
	body  string
	err   error
}

func (m Model) fetchLogs(target, title string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := client.Logs(ctx, target)
		if err != nil {
			return logsLoadedMsg{title: title, err: err}
		}
		return logsLoadedMsg{title: title, body: resp.Output}
	}
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
	seq, opts := tuiResolveDetachOpts(e.stderr)

	// Replay history before going live.
	replayCtx, replayCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if logs, err := e.client.Logs(replayCtx, e.target); err == nil && logs.Output != "" {
		fmt.Fprint(e.stdout, logs.Output)
	}
	replayCancel()

	stream, err := e.client.Attach(context.Background(), e.target)
	if err != nil {
		fmt.Fprintln(e.stderr, "attach:", err)
		return err
	}
	fmt.Fprintln(e.stdout, ptyproxy.Banner(e.target, seq))
	return ptyproxy.Run(stream, opts...)
}

// tuiResolveDetachOpts mirrors cmd.resolveDetachOpts. Lives here to
// avoid a tui→cmd dependency.
func tuiResolveDetachOpts(stderr io.Writer) ([]byte, []ptyproxy.Option) {
	raw := os.Getenv("GODO_DETACH")
	if raw == "" {
		return ptyproxy.DefaultDetachSequence, nil
	}
	seq, err := ptyproxy.ParseDetachSequence(raw)
	if err != nil {
		fmt.Fprintf(stderr, "godo: GODO_DETACH=%q: %v; falling back to default\n", raw, err)
		return ptyproxy.DefaultDetachSequence, nil
	}
	return seq, []ptyproxy.Option{ptyproxy.WithDetachSequence(seq)}
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

	case logsLoadedMsg:
		if msg.err != nil {
			m.statusMsg = "logs: " + msg.err.Error()
			return m, clearStatusAfter(3 * time.Second)
		}
		// Strip a trailing newline so the viewer doesn't render an
		// empty bottom row for every log.
		body := strings.TrimRight(msg.body, "\n")
		if body == "" {
			m.logLines = []string{"(no output)"}
		} else {
			m.logLines = strings.Split(body, "\n")
		}
		m.logTitle = msg.title
		m.logScroll = 0
		// Jump to the bottom — the most recent output is what failed-job
		// users want to see first. They can scroll up with k/PgUp.
		m.logScroll = m.maxLogScroll()
		m.mode = modeLogs
		return m, nil
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeLogs {
		return m.handleKeyLogs(msg)
	}
	// A pending delete-confirm consumes the next keypress: y/Y commits,
	// anything else cancels. Handled before the regular keymap so 'd'
	// twice in a row asks then commits, not asks then re-prompts.
	if m.pendingDelete != "" {
		hash := m.pendingDelete
		m.pendingDelete = ""
		switch msg.String() {
		case "y", "Y":
			return m, m.remove(hash)
		default:
			m.statusMsg = "remove cancelled"
			return m, clearStatusAfter(2 * time.Second)
		}
	}

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
	case "p":
		if j, ok := m.currentJob(); ok {
			switch j.State {
			case job.Running:
				return m, m.pause(j.Hash)
			case job.Paused:
				return m, m.resume(j.Hash)
			default:
				m.statusMsg = "can only pause/resume a running or paused job"
				return m, clearStatusAfter(2 * time.Second)
			}
		}
	case "d":
		if j, ok := m.currentJob(); ok {
			if j.State == job.Running || j.State == job.Paused {
				m.statusMsg = "stop the job first (K), then d to remove"
				return m, clearStatusAfter(3 * time.Second)
			}
			m.pendingDelete = j.Hash
			return m, nil
		}
	case "enter":
		if j, ok := m.currentJob(); ok {
			if j.State == job.Running {
				return m, tea.Exec(
					&attachExec{client: m.client, target: j.Hash},
					func(err error) tea.Msg { return attachExitedMsg{err: err} },
				)
			}
			// Non-running: show the persisted log in an in-TUI viewer.
			title := fmt.Sprintf("%s  %s  [%s]", j.ShortID(), j.Name, j.State)
			return m, tea.Batch(
				m.flashStatus("loading log…"),
				m.fetchLogs(j.Hash, title),
			)
		}

	case "?":
		m.statusMsg = "j/k=move  r=restart  p=pause/resume  K=kill  d=remove  enter=attach/logs  q=quit"
		return m, clearStatusAfter(4 * time.Second)
	}

	return m, nil
}

// handleKeyLogs is the key dispatcher for log-viewer mode. q/esc go
// back to the dashboard; movement keys mirror vim/less conventions.
func (m Model) handleKeyLogs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		m.mode = modeDashboard
		return m, nil
	case "j", "down":
		m.logScroll = clamp(m.logScroll+1, 0, m.maxLogScroll())
	case "k", "up":
		m.logScroll = clamp(m.logScroll-1, 0, m.maxLogScroll())
	case "ctrl+d", "pgdown", " ":
		m.logScroll = clamp(m.logScroll+m.logViewportHeight()/2, 0, m.maxLogScroll())
	case "ctrl+u", "pgup":
		m.logScroll = clamp(m.logScroll-m.logViewportHeight()/2, 0, m.maxLogScroll())
	case "g", "home":
		m.logScroll = 0
	case "G", "end":
		m.logScroll = m.maxLogScroll()
	}
	return m, nil
}

// logViewportHeight is the number of log lines that fit between the
// header and footer chrome.
func (m Model) logViewportHeight() int {
	const chrome = 4 // title + blank + footer + a margin row
	h := m.height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) maxLogScroll() int {
	max := len(m.logLines) - m.logViewportHeight()
	if max < 0 {
		return 0
	}
	return max
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

func (m Model) pause(hash string) tea.Cmd {
	client := m.client
	return tea.Batch(
		m.flashStatus("pausing…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Pause(ctx, hash); err != nil {
				return jobsLoadedMsg{err: err}
			}
			return nil
		},
		m.fetchJobs(),
	)
}

func (m Model) resume(hash string) tea.Cmd {
	client := m.client
	return tea.Batch(
		m.flashStatus("resuming…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Resume(ctx, hash); err != nil {
				return jobsLoadedMsg{err: err}
			}
			return nil
		},
		m.fetchJobs(),
	)
}

func (m Model) remove(hash string) tea.Cmd {
	client := m.client
	return tea.Batch(
		m.flashStatus("removing…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Remove(ctx, hash); err != nil {
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

	cmd := commandLine(j)
	name := j.Name
	// When --name wasn't given, Job.Name defaults to the full command
	// line, which would just duplicate the CMD column. Collapse to "*"
	// so the CMD column is the only place the command shows.
	if name == cmd {
		name = "*"
	}

	return prefix +
		padRight(j.ShortID(), 8) + "  " +
		padRight(truncRunes(name, 20), 20) + "  " +
		padRight(truncRunes(cmd, 28), 28) + "  " +
		padRight(string(j.State), 10) + "  " +
		padRight(fmt.Sprintf("%d", j.PID), 7) + "  " +
		padRight(uptime, 8) + "  " +
		exit
}

// commandLine renders Command + Args as a single string, the way a shell
// would have invoked it. Shown in the TUI's CMD column so a user-given
// --name doesn't hide what's actually running.
func commandLine(j job.Job) string {
	if len(j.Args) == 0 {
		return j.Command
	}
	return j.Command + " " + strings.Join(j.Args, " ")
}

// padRight pads s with spaces to exactly width display columns, using
// rune count (not byte count, which fmt's %-Ns does — that under-pads
// any string containing multi-byte runes like our truncation ellipsis).
// Strings already at or above width are returned unchanged.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// truncRunes truncates s to at most width runes, replacing the last
// rune with "…" when truncation occurs. The result is always exactly
// width runes when truncation happens, so padRight gives a stable
// column.
func truncRunes(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	statusStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)

// View is the Bubble Tea renderer.
func (m Model) View() string {
	if m.mode == modeLogs {
		return m.viewLogs()
	}
	return m.viewDashboard()
}

func (m Model) viewDashboard() string {
	var b strings.Builder

	header := fmt.Sprintf("  %-8s  %-20s  %-28s  %-10s  %-7s  %-8s  %s",
		"ID", "NAME", "CMD", "STATE", "PID", "UPTIME", "EXIT")
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
	hint := "j/k move · r restart · p pause/resume · K kill · d remove · enter attach/logs · q quit"
	if m.pendingDelete != "" {
		// Surface the confirm prompt prominently. The next keypress
		// is consumed by handleKey before the regular keymap runs.
		name := "job"
		for _, j := range m.jobs {
			if j.Hash == m.pendingDelete {
				name = j.Name
				break
			}
		}
		hint = fmt.Sprintf("Remove %s? [y/N]", name)
	} else if m.statusMsg != "" {
		hint = m.statusMsg
	}
	b.WriteString(statusStyle.Render("  " + hint))
	b.WriteString("\n")
	return b.String()
}

// viewLogs renders the in-TUI log viewer for a non-running job.
// Slices logLines to the viewport window starting at logScroll.
func (m Model) viewLogs() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("  logs · " + m.logTitle))
	b.WriteString("\n")

	height := m.logViewportHeight()
	end := m.logScroll + height
	if end > len(m.logLines) {
		end = len(m.logLines)
	}
	for i := m.logScroll; i < end; i++ {
		b.WriteString("  ")
		b.WriteString(m.logLines[i])
		b.WriteString("\n")
	}
	// Pad so the footer sits at a stable position even when the log
	// is shorter than the viewport.
	for i := end - m.logScroll; i < height; i++ {
		b.WriteString("\n")
	}

	pos := fmt.Sprintf("%d-%d/%d", m.logScroll+1, end, len(m.logLines))
	hint := fmt.Sprintf("j/k scroll · g/G top/bottom · PgUp/PgDn page · q back · %s", pos)
	b.WriteString(statusStyle.Render("  " + hint))
	b.WriteString("\n")
	return b.String()
}
