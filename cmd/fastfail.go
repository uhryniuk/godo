package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/uhryniuk/godo/internal/job"
	"github.com/uhryniuk/godo/internal/proto"
)

// fastFailWindow is how long we wait after a successful Run before
// deciding the child is staying up. Long enough to catch the dominant
// fast-fail modes (missing binary, bad flags, immediate panic) and
// short enough that fire-and-forget invocations don't feel sluggish.
const fastFailWindow = 500 * time.Millisecond

// awaitFastFail polls List for hash until it's terminal or the window
// elapses. Returns the terminal Job and true on a fast exit, or the
// zero Job and false if it's still running when the window closes.
//
// Used by `godo` (root) and `godo run` to surface failures inline:
// when a child dies in the first ~half-second (exec error, bad
// argument, immediate crash), the user gets the captured output on
// stderr instead of having to chase `godo logs <hash>` after the fact.
func awaitFastFail(ctx context.Context, c *proto.Client, hash string) (job.Job, bool) {
	deadline := time.Now().Add(fastFailWindow)
	for time.Now().Before(deadline) {
		list, err := c.List(ctx)
		if err != nil {
			return job.Job{}, false
		}
		for _, j := range list.Jobs {
			if j.Hash != hash {
				continue
			}
			if j.State.IsExited() {
				return j, true
			}
			break
		}
		select {
		case <-ctx.Done():
			return job.Job{}, false
		case <-time.After(40 * time.Millisecond):
		}
	}
	return job.Job{}, false
}

// reportIfFastFail surfaces an early termination: prints a header to
// stderr describing why the job died and tail-prints the captured
// combined output. Returns the exit code the CLI should propagate
// (0 if nothing to do).
func reportIfFastFail(ctx context.Context, c *proto.Client, hash string, w io.Writer) int {
	dead, ok := awaitFastFail(ctx, c, hash)
	if !ok {
		return 0
	}
	if dead.State == job.Completed && dead.ExitCode == 0 {
		// The child ran cleanly and exited inside our window. Don't
		// be noisy — but a short hint is friendly so users don't
		// wonder where their one-shot went.
		dur := dead.ExitedAt.Sub(dead.StartedAt).Round(time.Millisecond)
		fmt.Fprintf(w, "godo: %s completed in %s\n", dead.Name, dur)
		return 0
	}
	fmt.Fprintf(w, "godo: %s exited %d (%s); captured output:\n",
		dead.Name, dead.ExitCode, dead.State)
	if logs, err := c.Logs(ctx, hash); err == nil {
		writeLastLines(w, logs.Output, 25)
	}
	fmt.Fprintf(w, "godo: full log: godo logs %s\n", shortOrName(dead))
	if dead.ExitCode != 0 {
		return dead.ExitCode
	}
	return 1
}

// shortOrName picks the most ergonomic identifier for the user — the
// custom name if one was set, otherwise the short hash.
func shortOrName(j job.Job) string {
	cmd := j.Command
	if len(j.Args) > 0 {
		cmd = j.Command + " " + strings.Join(j.Args, " ")
	}
	if j.Name != "" && j.Name != cmd {
		return j.Name
	}
	return j.ShortID()
}

// writeLastLines prints the last n lines of body to w, prefixed with
// two spaces to visually nest them under the godo: header.
func writeLastLines(w io.Writer, body string, n int) {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		fmt.Fprintln(w, "  (no output captured)")
		return
	}
	lines := strings.Split(body, "\n")
	start := 0
	if len(lines) > n {
		start = len(lines) - n
		fmt.Fprintf(w, "  … (%d earlier lines)\n", start)
	}
	for _, line := range lines[start:] {
		fmt.Fprintln(w, "  "+line)
	}
}
