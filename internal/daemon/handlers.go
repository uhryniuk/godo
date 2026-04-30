package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
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
	default:
		return errf("unknown op: %s", req.Op)
	}
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
	stdout, _ := os.ReadFile(filepath.Join(j.LogDir, "stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(j.LogDir, "stderr.log"))
	return ok(proto.LogsResponse{Stdout: string(stdout), Stderr: string(stderr)})
}

// onJobExit applies the restart policy. Called from runner.watch after
// the terminal state is recorded.
func (d *Daemon) onJobExit(hash string, exitCode int, state job.State) {
	if state == job.Cancelled {
		return // user-requested stop, no auto restart
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
