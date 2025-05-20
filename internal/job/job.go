// Package job provides functionality for defining and managing system jobs,
// including their metadata, process ID, execution state, and output handling.
package job

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
  config "github.com/uhryniuk/godo/internal/config"
)

// JobState represents the execution state of a Job.
type JobState string

const (
	// Pending indicates that the job has been defined but not started.
	Pending JobState = "pending"

	// Running indicates that the job is currently executing.
	Running JobState = "running"

	// Completed indicates that the job finished successfully.
	Completed JobState = "completed"

	// Failed indicates that the job terminated with an error.
	Failed JobState = "failed"

	// Cancelled indicates that the job was stopped before completion.
	Cancelled JobState = "cancelled"
)

// Job represents a process managed by the CLI tool.
// It includes metadata such as name, command, arguments, output paths,
// process ID, a unique hash identifier, and its current execution state.
type Job struct {
	Hash       string   // Unique identifier for the job (SHA-256)
	Name       string // Human-readable name for the job
	Command    string   // Command to be executed
	Args       []string // Arguments to pass to the command
	StdoutPath string   // File path for redirecting standard output
	StderrPath string   // File path for redirecting standard error
	PID        int      // Process ID (set when the job is started)
	State      JobState // Current state of the job
}

// NewJob creates a new Job with the provided name, command, arguments,
// and file paths for stdout and stderr. It initializes the job with
// a unique hash, a default PID of 0, and a state of Pending.
func NewJob(command string, args []string) *Job {
	hash := generateJobHash(command, args)
	return &Job{
		Hash:       hash,
		Name:       strings.Join(append([]string{command}, args...), " "),
		Command:    command,
		Args:       args,
		StdoutPath: filepath.Join(config.GetJobDir(), hash, "stdout"),
		StderrPath: filepath.Join(config.GetJobDir(), hash, "stderr"),
		PID:        0,       // Not started yet
		State:      Pending, // Default initial state
	}
}

// generateJobHash creates a SHA-256 hash from the job's command, arguments,
// and the current timestamp to uniquely identify it.
func generateJobHash(command string, args []string) string {
	// Get the current timestamp
	timestamp := time.Now().UnixNano()

	// Create a string from the command, arguments, and timestamp
	data := fmt.Sprintf("%s:%s:%d", command, strings.Join(args, " "), timestamp)

	// Generate the hash
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

type JobOption func(*Job)


func WithName(name string) JobOption {
  return func(j *Job) {
    j.Name = name
  }
}

func WithStdoutPath(path string) JobOption {
  return func(j *Job) {
    j.StdoutPath = path
  }
}


func WithStderrPath(path string) JobOption {
  return func(j *Job) {
    j.StderrPath = path
  }
}

func WithPID(pid int) JobOption {
  return func(j *Job) {
    j.PID = pid
  }
}

func WithState(state JobState) JobOption {
  return func(j *Job) {
    j.State = state
  }
}
