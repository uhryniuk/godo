package proto

import (
	"encoding/json"

	"github.com/uhryniuk/godo/internal/job"
)

// Op identifies an RPC method on the daemon.
type Op string

const (
	OpPing Op = "Ping"
	OpRun  Op = "Run"
	OpList Op = "List"
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
