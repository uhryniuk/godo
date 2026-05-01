package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Client is a thin wrapper that opens a fresh Unix-socket connection per Call.
type Client struct {
	SocketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{SocketPath: socketPath}
}

// Dial opens a Unix-socket connection to the daemon. The caller owns Close.
func (c *Client) Dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", c.SocketPath)
}

// Call performs one request/response round-trip on a fresh connection.
func (c *Client) Call(ctx context.Context, req Request) (*Response, error) {
	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := WriteFrame(conn, req); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	var resp Response
	if err := ReadFrame(conn, &resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &resp, nil
}

// Ping returns the daemon's PingResponse, or an error if the daemon is
// unreachable or returns a non-OK reply.
func (c *Client) Ping(ctx context.Context) (*PingResponse, error) {
	var out PingResponse
	if err := c.callTyped(ctx, Request{Op: OpPing}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Run launches a job. Returns the registered Job (with PID populated).
func (c *Client) Run(ctx context.Context, req RunRequest) (*RunResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var out RunResponse
	if err := c.callTyped(ctx, Request{Op: OpRun, Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns a snapshot of every registered job.
func (c *Client) List(ctx context.Context) (*ListResponse, error) {
	var out ListResponse
	if err := c.callTyped(ctx, Request{Op: OpList}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Stop SIGTERMs the running process associated with target (name or hash
// prefix). Idempotent for already-stopped jobs.
func (c *Client) Stop(ctx context.Context, target string) (*StopResponse, error) {
	body, _ := json.Marshal(TargetRequest{Target: target})
	var out StopResponse
	if err := c.callTyped(ctx, Request{Op: OpStop, Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Restart stops the job (if running) and starts a fresh instance with the
// same spec. The returned Job carries the new PID and StartedAt.
func (c *Client) Restart(ctx context.Context, target string) (*RestartResponse, error) {
	body, _ := json.Marshal(TargetRequest{Target: target})
	var out RestartResponse
	if err := c.callTyped(ctx, Request{Op: OpRestart, Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Remove drops a stopped job from the registry and deletes its log dir.
// Errors if the job is still running or pending.
func (c *Client) Remove(ctx context.Context, target string) (*RemoveResponse, error) {
	body, _ := json.Marshal(TargetRequest{Target: target})
	var out RemoveResponse
	if err := c.callTyped(ctx, Request{Op: OpRemove, Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LoadService imports a TOML service file at path into the daemon's
// services dir and registers it.
func (c *Client) LoadService(ctx context.Context, path string) (*LoadServiceResponse, error) {
	body, _ := json.Marshal(LoadServiceRequest{Path: path})
	var out LoadServiceResponse
	if err := c.callTyped(ctx, Request{Op: OpLoadService, Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReloadServices rescans the services dir and applies changes.
func (c *Client) ReloadServices(ctx context.Context) (*ReloadServicesResponse, error) {
	var out ReloadServicesResponse
	if err := c.callTyped(ctx, Request{Op: OpReloadServices}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListServices returns the daemon's current view of loaded services.
func (c *Client) ListServices(ctx context.Context) (*ListServicesResponse, error) {
	var out ListServicesResponse
	if err := c.callTyped(ctx, Request{Op: OpListServices}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Logs returns the full contents of the job's combined output.log
// (PTY-merged stdout and stderr).
func (c *Client) Logs(ctx context.Context, target string) (*LogsResponse, error) {
	body, _ := json.Marshal(TargetRequest{Target: target})
	var out LogsResponse
	if err := c.callTyped(ctx, Request{Op: OpLogs, Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Attach opens a bidirectional PTY-proxy stream to target. Returns an
// AttachStream that implements io.ReadWriter — Read returns PTY output
// bytes from the daemon, Write sends PTY input bytes back. Resize sends
// a window-size update. Close hangs up the connection cleanly.
func (c *Client) Attach(ctx context.Context, target string) (*AttachStream, error) {
	body, _ := json.Marshal(TargetRequest{Target: target})
	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := WriteFrame(conn, Request{Op: OpAttach, Body: body}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write attach req: %w", err)
	}
	var ack Response
	if err := ReadFrame(conn, &ack); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read attach ack: %w", err)
	}
	if !ack.OK {
		_ = conn.Close()
		return nil, fmt.Errorf("daemon: %s", ack.Error)
	}
	return &AttachStream{conn: conn}, nil
}

// AttachStream is the client side of an OpAttach connection.
type AttachStream struct {
	conn    net.Conn
	readBuf []byte // leftover bytes from a previous DataFrame

	writeMu sync.Mutex // serializes Write and Resize on the wire
}

// Read implements io.Reader. Returns PTY output bytes from the daemon.
// On a clean close from the daemon (EOF frame), returns io.EOF.
func (s *AttachStream) Read(p []byte) (int, error) {
	if len(s.readBuf) > 0 {
		n := copy(p, s.readBuf)
		s.readBuf = s.readBuf[n:]
		return n, nil
	}
	for {
		var df DataFrame
		if err := ReadFrame(s.conn, &df); err != nil {
			return 0, err
		}
		if df.EOF {
			return 0, io.EOF
		}
		if len(df.Data) == 0 {
			continue // resize-only or empty frame; keep reading
		}
		n := copy(p, df.Data)
		if n < len(df.Data) {
			s.readBuf = df.Data[n:]
		}
		return n, nil
	}
}

// Write implements io.Writer. Sends PTY input bytes to the daemon.
func (s *AttachStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := WriteFrame(s.conn, DataFrame{Data: p}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize sends a window-size update to the daemon.
func (s *AttachStream) Resize(cols, rows uint16) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return WriteFrame(s.conn, DataFrame{Resize: &Size{Cols: cols, Rows: rows}})
}

// Close sends an EOF frame and closes the underlying connection.
func (s *AttachStream) Close() error {
	s.writeMu.Lock()
	_ = WriteFrame(s.conn, DataFrame{EOF: true})
	s.writeMu.Unlock()
	return s.conn.Close()
}

// LogsFollow opens a streaming connection that first replays existing
// log content then forwards live writes. fn is called for every chunk.
// Returns when the daemon sends an EOF frame, the connection breaks,
// ctx is cancelled, or fn returns an error.
func (c *Client) LogsFollow(ctx context.Context, target string, fn func(chunk []byte) error) error {
	body, _ := json.Marshal(TargetRequest{Target: target})

	conn, err := c.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Cancel via ctx by closing the conn from a goroutine.
	cancelCh := make(chan struct{})
	defer close(cancelCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-cancelCh:
		}
	}()

	if err := WriteFrame(conn, Request{Op: OpLogsFollow, Body: body}); err != nil {
		return err
	}
	var ack Response
	if err := ReadFrame(conn, &ack); err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("daemon: %s", ack.Error)
	}

	for {
		var df DataFrame
		if err := ReadFrame(conn, &df); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(df.Data) > 0 {
			if err := fn(df.Data); err != nil {
				return err
			}
		}
		if df.EOF {
			return nil
		}
	}
}

// callTyped is a helper that performs Call, checks OK, and unmarshals
// resp.Body into out.
func (c *Client) callTyped(ctx context.Context, req Request, out any) error {
	resp, err := c.Call(ctx, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon: %s", resp.Error)
	}
	if out == nil || len(resp.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Reachable returns true if the daemon answers a Ping within timeout.
func (c *Client) Reachable(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := c.Ping(ctx)
	if err == nil {
		return true
	}
	// A connection-refused or no-such-file error means not reachable; we want
	// to distinguish from a daemon that answered but errored. Future steps may
	// inspect the error type more carefully.
	var nerr *net.OpError
	if errors.As(err, &nerr) {
		return false
	}
	return false
}
