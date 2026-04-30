// Package job defines the Job model and its state machine. A Job is the
// daemon's record of one supervised process: identity, command, lifecycle
// state, and policy. Jobs are persisted as JSON; do not put non-encodable
// fields here (file handles, channels, etc) — those live alongside in the
// daemon's in-memory RunningJob.
package job

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/uhryniuk/godo/internal/config"
)

// State is the execution state of a Job.
type State string

const (
	Pending   State = "pending"
	Running   State = "running"
	Completed State = "completed"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

// CanTransition reports whether moving from s to next is a legal step in
// the lifecycle.
//
//	Pending                       -> Running, Cancelled
//	Running                       -> Completed, Failed, Cancelled
//	Completed, Failed, Cancelled  -> Pending  (restart, manual or by policy)
func (s State) CanTransition(next State) bool {
	switch s {
	case Pending:
		return next == Running || next == Cancelled
	case Running:
		return next == Completed || next == Failed || next == Cancelled
	case Completed, Failed, Cancelled:
		return next == Pending
	}
	return false
}

// IsExited reports whether s is one of the post-execution states. The
// reaper consults this to decide whether the restart policy should fire.
// Cancelled counts as exited but is never auto-restarted (user intent).
func (s State) IsExited() bool {
	return s == Completed || s == Failed || s == Cancelled
}

// RestartPolicy controls what the reaper does when a Running job exits.
type RestartPolicy string

const (
	RestartNo        RestartPolicy = "no"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

// ShouldRestart returns true if a job that exited with the given exit code
// should be restarted under this policy.
func (p RestartPolicy) ShouldRestart(exitCode int) bool {
	switch p {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return exitCode != 0
	default:
		return false
	}
}

// Job is the persisted record of one supervised process.
type Job struct {
	Hash    string   `json:"hash"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	PID     int      `json:"pid"`
	State   State    `json:"state"`

	StartedAt    time.Time `json:"started_at,omitempty"`
	ExitedAt     time.Time `json:"exited_at,omitempty"`
	ExitCode     int       `json:"exit_code"`
	RestartCount int       `json:"restart_count"`

	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`

	Nice    int           `json:"nice,omitempty"`
	IOnice  string        `json:"ionice,omitempty"`
	Restart RestartPolicy `json:"restart,omitempty"`

	CronSchedule string `json:"cron_schedule,omitempty"`
	ServiceFile  string `json:"service_file,omitempty"`

	LogDir string `json:"log_dir"`

	// v2 hooks. Always empty in v1.
	PipedFrom []string `json:"piped_from,omitempty"`
	PipedTo   []string `json:"piped_to,omitempty"`
}

// New creates a Pending Job with a fresh hash and the conventional LogDir.
// Apply Option helpers to set anything else.
func New(command string, args []string, opts ...Option) *Job {
	hash := generateHash(command, args)
	j := &Job{
		Hash:    hash,
		Name:    strings.Join(append([]string{command}, args...), " "),
		Command: command,
		Args:    args,
		State:   Pending,
		Restart: RestartNo,
		LogDir:  filepath.Join(config.GetStateDir(), hash),
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

func generateHash(command string, args []string) string {
	data := fmt.Sprintf("%s:%s:%d", command, strings.Join(args, " "), time.Now().UnixNano())
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// ShortID returns the first 8 hex chars of the Hash for compact display.
func (j *Job) ShortID() string {
	if len(j.Hash) >= 8 {
		return j.Hash[:8]
	}
	return j.Hash
}

// Option configures a Job at construction time.
type Option func(*Job)

func WithName(name string) Option      { return func(j *Job) { j.Name = name } }
func WithPID(pid int) Option           { return func(j *Job) { j.PID = pid } }
func WithState(s State) Option         { return func(j *Job) { j.State = s } }
func WithWorkingDir(dir string) Option { return func(j *Job) { j.WorkingDir = dir } }
func WithEnv(env map[string]string) Option {
	return func(j *Job) { j.Env = env }
}
func WithNice(n int) Option              { return func(j *Job) { j.Nice = n } }
func WithIOnice(s string) Option         { return func(j *Job) { j.IOnice = s } }
func WithRestart(p RestartPolicy) Option { return func(j *Job) { j.Restart = p } }
func WithCron(schedule string) Option    { return func(j *Job) { j.CronSchedule = schedule } }
func WithServiceFile(path string) Option { return func(j *Job) { j.ServiceFile = path } }
func WithLogDir(dir string) Option       { return func(j *Job) { j.LogDir = dir } }
