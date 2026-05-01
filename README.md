# godo

A small CLI for running and managing long-lived background processes — like `pueue` plus per-process PTY attach, in a single static Go binary.

`godo` runs a command as a supervised background process that survives shell logout. Listing, restarting, stopping, and tailing logs all go through one local daemon over a Unix socket. The daemon auto-spawns the first time you run anything, so installation is just dropping the binary on `$PATH`.

## Status

Pre-1.0, **in progress**. The CLI surface below works end-to-end on Linux and macOS. Declarative service files, cron, and the TUI are not in yet — see [Roadmap](#roadmap).

## Build

```sh
git clone https://github.com/uhryniuk/godo
cd godo
go build -o godo .
```

Requires Go 1.25+. Ships a single static binary; drop it anywhere on `$PATH`.

## Quick tour

```sh
# Run a command in the background. Returns the job id and pid.
godo sh -c "while true; do date; sleep 1; done"
# d2a91e0c  pid=12345  sh -c while true; do date; sleep 1; done

# List everything the daemon knows about.
godo list
# ID        NAME                                           STATE    PID    UPTIME  EXIT
# d2a91e0c  sh -c while true; do date; sleep 1; done       running  12345  4s      -

# Tail the combined output (PTY merges stdout + stderr).
godo logs d2a91e0c
godo logs -f d2a91e0c        # follow: replays + streams live; Ctrl+C to detach

# Drop into the job's PTY. Type to send to its stdin; see what it prints.
# Detach with Ctrl+B then 'd' (the job keeps running).
godo attach d2a91e0c

# Stop, restart, remove.
godo stop d2a91e0c           # SIGTERM, marks Cancelled
godo restart d2a91e0c        # same hash, fresh PID
godo rm d2a91e0c             # only when stopped; deletes the log dir

# Auto-restart on failure (or always):
godo --restart on-failure ./flaky-server
```

Targets accept either an exact name or a hash prefix.

## How it works

```
   CLI invocation                     Supervisor daemon (auto-spawned)
   ┌─────────────┐    Unix socket    ┌──────────────────────────────┐
   │ godo run … │ ─────────────────► │ accept → dispatch → respond │
   └─────────────┘  length-prefixed  │                              │
                        JSON         │  Registry  Runner  Reaper   │
                                     │     │        │       │       │
                                     │     ▼        ▼       ▼       │
                                     │  registry.json   /bin/sh ... │
                                     │                    │ PTY     │
                                     │                    ▼         │
                                     │             Multiplexer ────┐│
                                     │             ├─► output.log  ││
                                     │             └─► logs -f tail││
                                     └──────────────────────────────┘
```

- One daemon per user, listening on `~/.godo/state/godo.sock`.
- First `godo` invocation acquires a flock on `~/.godo/state/godo.sock.lock`, double-forks `godo supervisor` with `Setsid`, and polls until the socket is ready. Concurrent invocations spawn exactly one daemon.
- Children run under a real PTY (`creack/pty`) so jobs see a TTY, and stdout/stderr land in one combined `output.log`.
- The output Multiplexer fans PTY reads out to the log writer plus any active `logs -f` and `attach` clients with a drop-on-full policy — slow consumers can never block the producer.
- An InputMerger sits in front of the PTY master for the input direction. Today only `attach` registers a source; the v2 `godo pipe` will register sending jobs as additional sources without touching the write path.
- Daemon state persists to `~/.godo/state/registry.json` (atomic write, corrupt-tolerant load) so a restarted daemon remembers what was previously running. Children themselves do not yet survive a daemon restart.
- Logout-only by design: jobs survive your shell exit but are gone after a reboot. No system-level installation required.

## v1 CLI

| Command                          | Behavior                                                               |
| -------------------------------- | ---------------------------------------------------------------------- |
| `godo <cmd> [args...]`           | Run `<cmd>` as a supervised job. Auto-spawns the daemon.               |
| `godo list` / `godo ps`          | Print a table of all jobs with state/pid/uptime/exit.                  |
| `godo stop <id\|name>`           | SIGTERM the job's process group; sets `cancelled`.                     |
| `godo restart <id\|name>`        | Stop, wait for exit, start again with the same spec and hash.          |
| `godo rm <id\|name>`             | Drop a stopped job from the registry and delete its log dir.           |
| `godo logs <id\|name>`           | Print the job's combined output.                                       |
| `godo logs -f <id\|name>`        | Stream the log: replays existing content then forwards live writes.    |
| `godo attach <id\|name>`         | Take over the job's PTY; type into stdin, see stdout. Ctrl+B d detaches. |
| `godo daemon`                    | Run the supervisor in the foreground (debug / dev).                    |

Hidden: `godo supervisor` is the double-fork target invoked by auto-spawn.

## Roadmap

These steps are designed but not shipped:

- **Step 7** — TOML service files. Drop `~/.godo/services/web.toml` (systemd-style) and `godo reload` picks it up. Per-service `autostart`, `restart`, `nice`, etc.
- **Step 8** — Internal cron scheduler. `[cron] schedule = "0 4 * * *"` in a service file fires the command on schedule.
- **Step 9** — `godo monit` / `godo -i`: Bubble Tea TUI dashboard with sortable rows, hotkeys for restart/kill/log-tail, and in-pane PTY attach.
- **Step 10** — `--nice` and `--ionice` flags, `--name` flag for `godo run`, `godo shutdown`, `godo version`.

Beyond v1: live job-to-job piping (`godo pipe A B` fanning A's output into B's input, with the TUI showing the wire). The daemon's output multiplexer and input merger are already shaped for this; no v1 refactor is needed.

## Where state lives

```
~/.godo/
├── config.toml           # user-level defaults (Step 7)
├── services/             # one declarative service per file (Step 7)
└── state/
    ├── godo.sock         # daemon listener
    ├── godo.sock.lock    # auto-spawn flock
    ├── registry.json     # snapshotted registry
    └── <hash>/
        └── output.log    # PTY-merged stdout + stderr
```

## Development

```sh
go test -race ./...        # unit + functional, with race detector
go vet ./...
gofmt -l .                 # exits clean if everything is formatted
```

CI on push runs all four on Linux + macOS. See `.github/workflows/test.yml`.
