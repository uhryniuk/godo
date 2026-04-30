package proto

import (
	"encoding/json"

	"github.com/uhryniuk/godo/internal/job"
)

// Op identifies an RPC method on the daemon.
type Op string

const (
	OpPing       Op = "Ping"
	OpRun        Op = "Run"
	OpList       Op = "List"
	OpStop       Op = "Stop"
	OpRestart    Op = "Restart"
	OpRemove     Op = "Remove"
	OpLogs       Op = "Logs"       // one-shot, returns full content
	OpLogsFollow Op = "LogsFollow" // streaming: replays then follows
)

// Request is the envelope for every CLI->daemon RPC.
type Request struct {
	Op   Op              `json:"op"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Response is the envelope for every daemon->CLI RPC reply.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
}

// PingResponse is the body of an OpPing reply.
type PingResponse struct {
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

// RunRequest is the body of an OpRun.
type RunRequest struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Name       string            `json:"name,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Nice       int               `json:"nice,omitempty"`
	Restart    string            `json:"restart,omitempty"`
}

// RunResponse is the body of an OpRun reply.
type RunResponse struct {
	Job job.Job `json:"job"`
}

// ListResponse is the body of an OpList reply.
type ListResponse struct {
	Jobs []job.Job `json:"jobs"`
}

// TargetRequest is shared by Stop, Restart, Remove, and Logs. Target is
// either a job name or a hash prefix.
type TargetRequest struct {
	Target string `json:"target"`
}

// StopResponse, RestartResponse, RemoveResponse all carry the affected job
// snapshot for display.
type StopResponse struct {
	Job job.Job `json:"job"`
}

type RestartResponse struct {
	Job job.Job `json:"job"`
}

type RemoveResponse struct {
	ID string `json:"id"`
}

// LogsResponse carries the contents of a job's combined output.log.
// The PTY merges stdout and stderr at the kernel level, so we have one
// stream rather than two.
type LogsResponse struct {
	Output string `json:"output"`
}

// DataFrame is the streaming chunk type used by OpLogsFollow and (later)
// OpAttach. EOF is set on the final frame so the client can return cleanly
// without polling for connection close.
type DataFrame struct {
	Data []byte `json:"data,omitempty"` // base64 in JSON
	EOF  bool   `json:"eof,omitempty"`
}
