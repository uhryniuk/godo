package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	default:
		return errf("unknown op: %s", req.Op)
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
