package job

import (
	"strings"
	"testing"
)

func TestStateCanTransition(t *testing.T) {
	cases := []struct {
		name string
		from State
		to   State
		ok   bool
	}{
		{"pending->running", Pending, Running, true},
		{"pending->cancelled", Pending, Cancelled, true},
		{"pending->completed", Pending, Completed, false},
		{"pending->failed", Pending, Failed, false},
		{"pending->pending", Pending, Pending, false},

		{"running->completed", Running, Completed, true},
		{"running->failed", Running, Failed, true},
		{"running->cancelled", Running, Cancelled, true},
		{"running->pending", Running, Pending, false},

		{"failed->pending", Failed, Pending, true},
		{"failed->running", Failed, Running, false},
		{"failed->completed", Failed, Completed, false},

		{"completed->pending", Completed, Pending, true},
		{"completed->running", Completed, Running, false},
		{"completed->cancelled", Completed, Cancelled, false},

		{"cancelled->pending", Cancelled, Pending, true},
		{"cancelled->running", Cancelled, Running, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.from.CanTransition(c.to); got != c.ok {
				t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.ok)
			}
		})
	}
}

func TestStateIsExited(t *testing.T) {
	for _, s := range []State{Completed, Failed, Cancelled} {
		if !s.IsExited() {
			t.Errorf("%s should be exited", s)
		}
	}
	for _, s := range []State{Pending, Running} {
		if s.IsExited() {
			t.Errorf("%s should NOT be exited", s)
		}
	}
}

func TestRestartPolicyShouldRestart(t *testing.T) {
	cases := []struct {
		policy RestartPolicy
		exit   int
		want   bool
	}{
		{RestartNo, 0, false},
		{RestartNo, 1, false},
		{RestartNo, 137, false},

		{RestartOnFailure, 0, false},
		{RestartOnFailure, 1, true},
		{RestartOnFailure, 137, true},

		{RestartAlways, 0, true},
		{RestartAlways, 1, true},
		{RestartAlways, 137, true},

		{RestartPolicy(""), 1, false}, // unknown policy treated as no
	}
	for _, c := range cases {
		got := c.policy.ShouldRestart(c.exit)
		if got != c.want {
			t.Errorf("policy=%q exit=%d: got %v, want %v", c.policy, c.exit, got, c.want)
		}
	}
}

func TestNewSetsDefaults(t *testing.T) {
	j := New("echo", []string{"hi"})
	if j.State != Pending {
		t.Errorf("default state: got %s, want pending", j.State)
	}
	if j.Restart != RestartNo {
		t.Errorf("default restart: got %s, want no", j.Restart)
	}
	if j.Hash == "" {
		t.Error("hash should be populated")
	}
	if j.LogDir == "" || !strings.Contains(j.LogDir, j.Hash) {
		t.Errorf("LogDir should contain hash: got %q", j.LogDir)
	}
	if j.Name == "" {
		t.Error("default name should be derived from command+args")
	}
}

func TestNewHashesAreUnique(t *testing.T) {
	// Same command+args called twice within nanoseconds should still
	// differ because the hash includes UnixNano.
	a := New("echo", []string{"a"})
	b := New("echo", []string{"a"})
	if a.Hash == b.Hash {
		t.Errorf("expected distinct hashes, got %s twice", a.Hash)
	}
}

func TestShortID(t *testing.T) {
	j := New("echo", nil)
	if got := j.ShortID(); len(got) != 8 {
		t.Errorf("ShortID len: got %d, want 8 (id=%q)", len(got), got)
	}
	short := &Job{Hash: "abc"}
	if short.ShortID() != "abc" {
		t.Errorf("short hash should pass through, got %q", short.ShortID())
	}
}

func TestOptionsApply(t *testing.T) {
	j := New("echo", nil,
		WithName("greeter"),
		WithWorkingDir("/tmp"),
		WithEnv(map[string]string{"X": "y"}),
		WithNice(5),
		WithRestart(RestartAlways),
		WithCron("*/5 * * * *"),
	)
	if j.Name != "greeter" {
		t.Errorf("Name: %q", j.Name)
	}
	if j.WorkingDir != "/tmp" {
		t.Errorf("WorkingDir: %q", j.WorkingDir)
	}
	if j.Env["X"] != "y" {
		t.Errorf("Env: %v", j.Env)
	}
	if j.Nice != 5 {
		t.Errorf("Nice: %d", j.Nice)
	}
	if j.Restart != RestartAlways {
		t.Errorf("Restart: %s", j.Restart)
	}
	if j.CronSchedule != "*/5 * * * *" {
		t.Errorf("CronSchedule: %s", j.CronSchedule)
	}
}
