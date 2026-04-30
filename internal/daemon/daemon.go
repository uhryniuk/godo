// Package daemon implements the godo supervisor daemon. It listens on a
// Unix socket, dispatches RPCs from the proto package, and (later) owns
// the lifecycle of every child process.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/uhryniuk/godo/internal/proto"
)

// Version is bumped when the wire protocol or daemon-visible behaviour changes.
const Version = "0.0.1"

// Daemon is the supervisor server. Construct with New and call Run to block.
type Daemon struct {
	socketPath string
	listener   net.Listener
	wg         sync.WaitGroup
}

func New(socketPath string) *Daemon {
	return &Daemon{socketPath: socketPath}
}

// Run starts the daemon. It blocks until ctx is cancelled or SIGTERM/SIGINT
// is received. The Unix socket file is removed on exit.
func (d *Daemon) Run(ctx context.Context) error {
	// Remove stale socket from a prior crashed daemon. If a live daemon owns
	// it, the Listen call below will fail and we exit cleanly.
	_ = os.Remove(d.socketPath)

	l, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", d.socketPath, err)
	}
	d.listener = l
	slog.Info("daemon listening", "socket", d.socketPath, "pid", os.Getpid())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	shutdownReason := make(chan string, 1)
	go func() {
		select {
		case sig := <-sigCh:
			shutdownReason <- "signal:" + sig.String()
		case <-ctx.Done():
			shutdownReason <- "context"
		}
		_ = l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			slog.Error("accept failed", "err", err)
			continue
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.handleConn(conn)
		}()
	}

	d.wg.Wait()
	_ = os.Remove(d.socketPath)
	slog.Info("daemon stopped", "reason", <-shutdownReason)
	return nil
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	var req proto.Request
	if err := proto.ReadFrame(conn, &req); err != nil {
		slog.Error("read request frame", "err", err)
		return
	}
	resp := d.dispatch(req)
	if err := proto.WriteFrame(conn, resp); err != nil {
		slog.Error("write response frame", "err", err)
	}
}

func (d *Daemon) dispatch(req proto.Request) proto.Response {
	switch req.Op {
	case proto.OpPing:
		return ok(proto.PingResponse{Version: Version, PID: os.Getpid()})
	default:
		return proto.Response{OK: false, Error: fmt.Sprintf("unknown op: %s", req.Op)}
	}
}

// ok wraps a body value as a successful Response. Marshal failure on a
// daemon-controlled type is treated as a programming error.
func ok(body any) proto.Response {
	raw, err := json.Marshal(body)
	if err != nil {
		return proto.Response{OK: false, Error: "internal: " + err.Error()}
	}
	return proto.Response{OK: true, Body: raw}
}
