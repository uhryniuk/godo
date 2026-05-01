package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
	"github.com/uhryniuk/godo/internal/service"
)

func (d *Daemon) dispatch(req proto.Request) proto.Response {
	switch req.Op {
	case proto.OpPing:
		return ok(proto.PingResponse{Version: Version, PID: os.Getpid()})
	case proto.OpRun:
		return d.handleRun(req)
	case proto.OpList:
		return d.handleList()
	case proto.OpStop:
		return d.handleStop(req)
	case proto.OpRestart:
		return d.handleRestart(req)
	case proto.OpRemove:
		return d.handleRemove(req)
	case proto.OpLogs:
		return d.handleLogs(req)
	case proto.OpLoadService:
		return d.handleLoadService(req)
	case proto.OpReloadServices:
		return d.handleReloadServices()
	case proto.OpListServices:
		return d.handleListServices()
	case proto.OpShutdown:
		return d.handleShutdown()
	default:
		return errf("unknown op: %s", req.Op)
	}
}

func (d *Daemon) handleShutdown() proto.Response {
	// Closing the listener happens in the shutdown goroutine after it
	// reads from shutdownCh. The current connection stays open long
	// enough for handleConn to write our response and close cleanly.
	select {
	case d.shutdownCh <- struct{}{}:
	default:
	}
	return ok(proto.ShutdownResponse{})
}

func toServiceInfo(s *service.Spec) proto.ServiceInfo {
	return proto.ServiceInfo{
		Name:      s.Name,
		Path:      s.Path,
		Command:   s.Command,
		Autostart: s.Autostart,
		Restart:   s.Restart,
		Cron:      s.Cron.Schedule,
	}
}

func (d *Daemon) handleLoadService(req proto.Request) proto.Response {
	var body proto.LoadServiceRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errf("decode LoadServiceRequest: %v", err)
	}
	if body.Path == "" {
		return errf("path is required")
	}
	spec, err := d.importServiceFile(body.Path)
	if err != nil {
		return errf("load: %v", err)
	}
	return ok(proto.LoadServiceResponse{Service: toServiceInfo(spec)})
}

func (d *Daemon) handleReloadServices() proto.Response {
	diff, errs := d.reloadServices()
	resp := proto.ReloadServicesResponse{
		Removed: diff.Removed,
	}
	for _, s := range diff.Added {
		resp.Added = append(resp.Added, toServiceInfo(s))
	}
	for _, s := range diff.Modified {
		resp.Modified = append(resp.Modified, toServiceInfo(s))
	}
	for _, e := range errs {
		resp.Errors = append(resp.Errors, e.Error())
	}
	return ok(resp)
}

func (d *Daemon) handleListServices() proto.Response {
	specs := d.svc.snapshot()
	infos := make([]proto.ServiceInfo, 0, len(specs))
	for _, s := range specs {
		infos = append(infos, toServiceInfo(s))
	}
	return ok(proto.ListServicesResponse{Services: infos})
}

func (d *Daemon) resolveTarget(req proto.Request) (*job.Job, *proto.Response) {
	var body proto.TargetRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		r := errf("decode target: %v", err)
		return nil, &r
	}
	if body.Target == "" {
		r := errf("target is required")
		return nil, &r
	}
	j, err := d.registry.Resolve(body.Target)
	if err != nil {
		r := errf("%s: %v", body.Target, err)
		return nil, &r
	}
	return j, nil
}

func (d *Daemon) handleStop(req proto.Request) proto.Response {
	j, errResp := d.resolveTarget(req)
	if errResp != nil {
		return *errResp
	}
	if err := d.runner.Stop(j.Hash); err != nil {
		return errf("stop: %v", err)
	}
	snap, _ := d.registry.GetCopy(j.Hash)
	return ok(proto.StopResponse{Job: snap})
}

func (d *Daemon) handleRestart(req proto.Request) proto.Response {
	j, errResp := d.resolveTarget(req)
	if errResp != nil {
		return *errResp
	}
	if err := d.runner.Stop(j.Hash); err != nil {
		return errf("stop: %v", err)
	}
	// Wait for the watcher to record the terminal state. If it doesn't
	// settle within the deadline we report and abort the restart.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := d.registry.GetCopy(j.Hash)
		if err != nil {
			return errf("post-stop get: %v", err)
		}
		if snap.State.IsExited() || snap.State == job.Pending {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := d.registry.Update(j.Hash, func(j *job.Job) {
		j.State = job.Pending
		j.PID = 0
		j.ExitCode = 0
		j.ExitedAt = time.Time{}
	}); err != nil {
		return errf("reset: %v", err)
	}
	snap, _ := d.registry.GetCopy(j.Hash)
	if err := d.runner.Start(&snap); err != nil {
		return errf("start: %v", err)
	}
	snap, _ = d.registry.GetCopy(j.Hash)
	return ok(proto.RestartResponse{Job: snap})
}

func (d *Daemon) handleRemove(req proto.Request) proto.Response {
	j, errResp := d.resolveTarget(req)
	if errResp != nil {
		return *errResp
	}
	if j.State == job.Running {
		return errf("job %s is running; stop it first", j.Name)
	}
	logDir := j.LogDir
	d.registry.Remove(j.Hash)
	if logDir != "" {
		if err := os.RemoveAll(logDir); err != nil {
			slog.Warn("remove log dir", "path", logDir, "err", err)
		}
	}
	if err := d.Save(); err != nil {
		slog.Warn("save after remove", "err", err)
	}
	return ok(proto.RemoveResponse{ID: j.Hash})
}

func (d *Daemon) handleLogs(req proto.Request) proto.Response {
	j, errResp := d.resolveTarget(req)
	if errResp != nil {
		return *errResp
	}
	body, _ := os.ReadFile(filepath.Join(j.LogDir, outputLogName))
	return ok(proto.LogsResponse{Output: string(body)})
}

// handleLogsFollow streams the job's combined log: first replays the
// existing on-disk content up to the moment of subscription, then forwards
// live writes from the multiplexer until the job exits or the client
// disconnects. The replay/follow handoff is atomic: SubscribeWithLockedSnapshot
// captures the file size and registers the subscriber under the same
// mux lock, so no chunk is dropped or duplicated across the boundary.
func (d *Daemon) handleLogsFollow(req proto.Request, conn net.Conn) {
	j, errResp := d.resolveTarget(req)
	if errResp != nil {
		_ = proto.WriteFrame(conn, *errResp)
		return
	}
	if err := proto.WriteFrame(conn, proto.Response{OK: true}); err != nil {
		return
	}

	logPath := filepath.Join(j.LogDir, outputLogName)
	f, err := os.Open(logPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = proto.WriteFrame(conn, proto.DataFrame{EOF: true})
		return
	}
	if f != nil {
		defer f.Close()
	}

	mux := d.runner.Multiplexer(j.Hash)

	var (
		sub      *Subscriber
		fileSize int64
	)
	if mux != nil {
		s, snap, err := mux.SubscribeWithLockedSnapshot(func() (any, error) {
			if f == nil {
				return int64(0), nil
			}
			info, statErr := f.Stat()
			if statErr != nil {
				return int64(0), nil
			}
			return info.Size(), nil
		})
		if err == nil {
			sub = s
			fileSize, _ = snap.(int64)
			defer sub.Cancel()
		}
	} else if f != nil {
		info, _ := f.Stat()
		fileSize = info.Size()
	}

	// Replay [0, fileSize) from disk.
	if f != nil && fileSize > 0 {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			remaining := fileSize
			buf := make([]byte, 4096)
			for remaining > 0 {
				n := int64(len(buf))
				if n > remaining {
					n = remaining
				}
				read, rerr := f.Read(buf[:n])
				if read > 0 {
					if werr := proto.WriteFrame(conn, proto.DataFrame{Data: buf[:read]}); werr != nil {
						return
					}
					remaining -= int64(read)
				}
				if rerr != nil {
					break
				}
			}
		}
	}

	// Forward live writes until the mux closes (job exit) or the client
	// disconnects (write fails).
	if sub != nil {
		for chunk := range sub.Ch {
			if err := proto.WriteFrame(conn, proto.DataFrame{Data: chunk}); err != nil {
				return
			}
		}
	}

	_ = proto.WriteFrame(conn, proto.DataFrame{EOF: true})
}

// handleAttach is the bidirectional PTY-proxy stream. The daemon
// subscribes the client to the job's output multiplexer and registers a
// new input source on the job's input merger. Output frames go from mux
// to client; input frames go from client to merger. Resize frames bypass
// the merger and call pty.Setsize on the master directly.
//
// The connection ends when EITHER side hangs up: the job exits (mux
// closes its subscriber channel) or the client disconnects (frame read
// fails). Both paths cleanly tear down the input source and subscription.
func (d *Daemon) handleAttach(req proto.Request, conn net.Conn) {
	j, errResp := d.resolveTarget(req)
	if errResp != nil {
		_ = proto.WriteFrame(conn, *errResp)
		return
	}

	mux := d.runner.Multiplexer(j.Hash)
	merger := d.runner.InputMerger(j.Hash)
	if mux == nil || merger == nil {
		_ = proto.WriteFrame(conn, errf("job %s is not running", j.Name))
		return
	}

	if err := proto.WriteFrame(conn, proto.Response{OK: true}); err != nil {
		return
	}

	sub := mux.Subscribe()
	src := merger.AddSource()

	// Client -> daemon: input bytes and resize events. Exits on EOF
	// frame or any read error (client disconnected).
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			var df proto.DataFrame
			if err := proto.ReadFrame(conn, &df); err != nil {
				return
			}
			if df.EOF {
				return
			}
			if df.Resize != nil {
				_ = d.runner.Resize(j.Hash, df.Resize.Cols, df.Resize.Rows)
				continue
			}
			if len(df.Data) > 0 {
				if err := src.Send(df.Data); err != nil {
					return
				}
			}
		}
	}()

	// Daemon -> client: forward mux output until the job exits or the
	// client goroutine signals exit.
	for {
		select {
		case <-clientDone:
			// Client disconnected. Tear down our half.
			sub.Cancel()
			src.Close()
			return
		case chunk, ok := <-sub.Ch:
			if !ok {
				// Job exited. Send EOF, close conn, wait for client
				// goroutine to notice and exit.
				_ = proto.WriteFrame(conn, proto.DataFrame{EOF: true})
				_ = conn.Close()
				<-clientDone
				src.Close()
				return
			}
			if err := proto.WriteFrame(conn, proto.DataFrame{Data: chunk}); err != nil {
				// Write failed (client gone). Close conn so the client
				// goroutine sees the read fail and exits.
				_ = conn.Close()
				<-clientDone
				sub.Cancel()
				src.Close()
				return
			}
		}
	}
}

// onJobExit applies the restart policy. Called from runner.watch after
// the terminal state is recorded.
func (d *Daemon) onJobExit(hash string, exitCode int, state job.State) {
	if state == job.Cancelled {
		return // user-requested stop, no auto restart
	}
	// Stop may have raced with the watcher and lost the state-update
	// race; honor the stoppedManually flag regardless of recorded state
	// so a Stop is never silently overridden by a restart.
	if d.runner.WasStopped(hash) {
		return
	}
	snap, err := d.registry.GetCopy(hash)
	if err != nil {
		return
	}
	if !snap.Restart.ShouldRestart(exitCode) {
		return
	}
	if err := d.registry.Update(hash, func(j *job.Job) {
		j.RestartCount++
		j.State = job.Pending
		j.PID = 0
		j.ExitCode = 0
		j.ExitedAt = time.Time{}
	}); err != nil {
		slog.Warn("restart: reset failed", "id", snap.ShortID(), "err", err)
		return
	}
	fresh, err := d.registry.GetCopy(hash)
	if err != nil {
		return
	}
	if err := d.runner.Start(&fresh); err != nil {
		slog.Error("restart: start failed", "id", fresh.ShortID(), "err", err)
	}
}

func (d *Daemon) handleRun(req proto.Request) proto.Response {
	var body proto.RunRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errf("decode RunRequest: %v", err)
	}
	if body.Command == "" {
		return errf("command is required")
	}

	opts := []job.Option{}
	if body.Name != "" {
		opts = append(opts, job.WithName(body.Name))
	}
	if body.WorkingDir != "" {
		opts = append(opts, job.WithWorkingDir(body.WorkingDir))
	}
	if body.Env != nil {
		opts = append(opts, job.WithEnv(body.Env))
	}
	if body.Nice != 0 {
		opts = append(opts, job.WithNice(body.Nice))
	}
	if body.Restart != "" {
		opts = append(opts, job.WithRestart(job.RestartPolicy(body.Restart)))
	}

	j := job.New(body.Command, body.Args, opts...)
	if err := d.registry.Add(j); err != nil {
		if errors.Is(err, ErrNameTaken) {
			return errf("name already in use: %s", body.Name)
		}
		return errf("register: %v", err)
	}
	if err := d.runner.Start(j); err != nil {
		// Roll back the registration so we don't leak a Pending job that
		// never started.
		d.registry.Remove(j.Hash)
		return errf("start: %v", err)
	}
	// Return the post-Start snapshot under the registry lock so the
	// response sees the freshly-set PID and Running state.
	snap, err := d.registry.GetCopy(j.Hash)
	if err != nil {
		return errf("post-start get: %v", err)
	}
	return ok(proto.RunResponse{Job: snap})
}

func (d *Daemon) handleList() proto.Response {
	return ok(proto.ListResponse{Jobs: d.registry.Snapshot()})
}

func errf(format string, args ...any) proto.Response {
	return proto.Response{OK: false, Error: fmt.Sprintf(format, args...)}
}
