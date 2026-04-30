package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
