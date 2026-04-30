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
	resp, err := c.Call(ctx, Request{Op: OpPing})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var ping PingResponse
	if err := json.Unmarshal(resp.Body, &ping); err != nil {
		return nil, fmt.Errorf("decode ping body: %w", err)
	}
	return &ping, nil
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
