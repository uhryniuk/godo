package proto

import "encoding/json"

// Op identifies an RPC method on the daemon.
type Op string

const (
	OpPing Op = "Ping"
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
