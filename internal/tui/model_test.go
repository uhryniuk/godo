package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uhryniuk/godo/internal/job"
)

// keyMsg constructs a tea.KeyMsg whose String() returns s. Supports
// single-rune inputs (e.g. "d", "y", "n") used in the dashboard
// keymap. For multi-character names like "enter", construct directly.
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Most of the TUI is rendering and Bubble Tea wiring (we don't test
// either by policy). The pure parts worth pinning are clamp() and the
// row formatter so visual changes that break layout are caught.

func TestClamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want int }{
		{0, 0, 5, 0},
		{3, 0, 5, 3},
		{10, 0, 5, 5},
		{-1, 0, 5, 0},
		{2, 5, 0, 5}, // hi < lo: returns lo
		{0, 0, -1, 0},
	}
	for _, c := range cases {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clamp(%d,%d,%d): got %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestTruncRunes(t *testing.T) {
	if got := truncRunes("short", 32); got != "short" {
		t.Errorf("short pass-through: %q", got)
	}
	long := strings.Repeat("a", 50)
	got := truncRunes(long, 32)
	if n := utf8.RuneCountInString(got); n != 32 {
		t.Errorf("truncated rune count: got %d, want 32", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated should end with ellipsis: %q", got)
	}
}

func TestPadRightRuneAware(t *testing.T) {
	// "abc…" is 4 runes / 6 bytes. Padding to 8 columns must add 4
	// spaces, so the visible width matches.
	got := padRight("abc…", 8)
	if n := utf8.RuneCountInString(got); n != 8 {
		t.Errorf("padded rune count: got %d, want 8 (%q)", n, got)
	}
}

func TestRenderRowNameStarsWhenSameAsCommand(t *testing.T) {
	// Default Job.Name is "<command> <args...>" which equals
	// commandLine(j). renderRow should collapse Name to "*".
	j := job.Job{
		Hash:    "abcdef1234567890",
		Name:    "/bin/sh -c sleep 30",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
		State:   job.Running,
	}
	row := renderRow(j, time.Now(), false)
	if !strings.Contains(row, " *  ") {
		t.Errorf("expected Name column to be '*' when matching CMD; row: %q", row)
	}
	if !strings.Contains(row, "/bin/sh -c sleep 30") {
		t.Errorf("CMD column should still show full command; row: %q", row)
	}
}

func TestRenderRowMarksSelectionAndIncludesFields(t *testing.T) {
	j := job.Job{
		Hash:      "abcdef1234567890",
		Name:      "web",
		State:     job.Running,
		PID:       42,
		StartedAt: time.Now().Add(-5 * time.Second),
	}
	row := renderRow(j, time.Now(), true)
	if !strings.HasPrefix(row, "> ") {
		t.Errorf("selected row should start with '> ': %q", row)
	}
	for _, want := range []string{"abcdef12", "web", "running", "42"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %q", want, row)
		}
	}
}

func TestRenderRowExitDashedForRunning(t *testing.T) {
	j := job.Job{
		Hash:  "deadbeef00000000",
		Name:  "x",
		State: job.Running,
	}
	row := renderRow(j, time.Now(), false)
	// Exit column for Running should not be a number — should be a
	// dash. Tabwriter could include "0" so check for the trailing "-".
	if !strings.HasSuffix(strings.TrimRight(row, " \n"), "-") {
		t.Errorf("running job exit should be -, got %q", row)
	}
}

// TestDeleteConfirmFlow exercises the d → y/N flow in handleKey
// without spinning up Bubble Tea. Asserts that the first 'd' arms a
// pending confirm, and that 'n' (or any non-y) cancels it.
func TestDeleteConfirmFlow(t *testing.T) {
	m := Model{
		jobs: []job.Job{{
			Hash:  "abcdef1234567890",
			Name:  "victim",
			State: job.Failed,
		}},
		selected: 0,
		mode:     modeDashboard,
	}

	updated, _ := m.handleKey(keyMsg("d"))
	mm := updated.(Model)
	if mm.pendingDelete == "" {
		t.Fatal("d should arm pendingDelete on a non-running job")
	}
	if mm.pendingDelete != "abcdef1234567890" {
		t.Errorf("pendingDelete = %q, want full hash", mm.pendingDelete)
	}

	// 'n' (anything but y/Y) cancels. Pending should be cleared.
	updated, _ = mm.handleKey(keyMsg("n"))
	mm = updated.(Model)
	if mm.pendingDelete != "" {
		t.Errorf("pendingDelete should be cleared after non-y, got %q", mm.pendingDelete)
	}
	if !strings.Contains(mm.statusMsg, "cancel") {
		t.Errorf("expected cancel status, got %q", mm.statusMsg)
	}
}

// TestDeleteRefusesRunning verifies the strict policy (matches CLI).
func TestDeleteRefusesRunning(t *testing.T) {
	m := Model{
		jobs: []job.Job{{
			Hash:  "abcdef1234567890",
			Name:  "live",
			State: job.Running,
		}},
		selected: 0,
		mode:     modeDashboard,
	}
	updated, _ := m.handleKey(keyMsg("d"))
	mm := updated.(Model)
	if mm.pendingDelete != "" {
		t.Error("d on a running job should not arm pendingDelete")
	}
	if !strings.Contains(mm.statusMsg, "stop") {
		t.Errorf("expected hint mentioning 'stop', got %q", mm.statusMsg)
	}
}

// TestAttachFailedFallsBackToLogViewer checks that when attach fails
// (e.g. a fast-exiting job was no longer running), the TUI switches
// to log-viewer mode rather than just showing an error status.
func TestAttachFailedFallsBackToLogViewer(t *testing.T) {
	m := Model{
		jobs: []job.Job{{
			Hash:  "abcdef1234567890",
			Name:  "ls",
			State: job.Completed,
		}},
		selected: 0,
		mode:     modeDashboard,
		width:    120,
		height:   40,
	}

	// Simulate an attachExitedMsg with a non-nil error (attach failed).
	updated, cmd := m.Update(attachExitedMsg{
		target: "abcdef1234567890",
		err:    fmt.Errorf("job ls is not running"),
	})
	mm := updated.(Model)

	// Should NOT change mode yet (logs are fetched async), but a cmd
	// should be returned (the fetchLogs tea.Cmd).
	if cmd == nil {
		t.Fatal("expected a tea.Cmd (fetchLogs) to be returned after attach failure")
	}
	// Mode should still be dashboard until logsLoadedMsg arrives.
	if mm.mode != modeDashboard {
		t.Errorf("mode should stay dashboard until logs load, got %v", mm.mode)
	}

	// Simulate the logs loading with some content.
	updated, _ = mm.Update(logsLoadedMsg{
		title: "abcdef12  ls  [completed]",
		body:  "file1.txt\nfile2.txt\n",
	})
	mm = updated.(Model)
	if mm.mode != modeLogs {
		t.Errorf("mode should be modeLogs after logsLoadedMsg, got %v", mm.mode)
	}
	if len(mm.logLines) == 0 || mm.logLines[0] == "(no output)" {
		t.Errorf("expected log lines to contain output, got %v", mm.logLines)
	}
}

func TestRenderRowExitNumericForExited(t *testing.T) {
	j := job.Job{
		Hash:     "deadbeef00000000",
		Name:     "x",
		State:    job.Failed,
		ExitCode: 7,
	}
	row := renderRow(j, time.Now(), false)
	if !strings.HasSuffix(strings.TrimRight(row, " \n"), "7") {
		t.Errorf("failed job should show numeric exit, got %q", row)
	}
}
