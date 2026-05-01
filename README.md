# godo

A small CLI for running and managing long-lived background processes — like `pueue` plus per-process PTY attach, in a single static Go binary.

`godo` runs a command as a supervised background process that survives shell logout. Listing, restarting, stopping, and tailing logs all go through one local daemon over a Unix socket. The daemon auto-spawns the first time you run anything, so installation is just dropping the binary on `$PATH`.

## Status

**v1 feature-complete** on Linux and macOS. v2 (live job-to-job piping, remote / agent-oriented surfaces, ...) starts after a dogfooding window — see [Roadmap](#roadmap).

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
# Default detach is Ctrl+X then d (chosen to avoid tmux/screen/zellij mode
# prefixes). Override with $GODO_DETACH, e.g. GODO_DETACH="Ctrl+B,d" if you
# prefer the tmux convention.
godo attach d2a91e0c

# Stop, restart, remove.
godo stop d2a91e0c           # SIGTERM, marks Cancelled
godo restart d2a91e0c        # same hash, fresh PID
godo rm d2a91e0c             # only when stopped; deletes the log dir

# godo does NOT shell-tokenize: don't quote the whole command.
#   bad:  godo "python3 -m http.server"   ← passed as one filename, exec fails
#   good: godo python3 -m http.server     ← shell tokenizes, godo passes through
#   good: godo sh -c "python3 -m http.server"   ← when you need shell features

# Name an ad-hoc command — pass --name (or -n) BEFORE the command. Anything
# after the command name is passed to the child untouched, so a --name in
# the child's own args is preserved.
godo --name web sh -c "python3 -m http.server 8888"
godo rm web

# For richer flags (--restart / --nice / --env / --working-dir), use the
# explicit form — `--` separates godo flags from the child's:
godo run --name web --restart on-failure -- node server.js --port 8080

# Then target by name:
godo stop web
godo logs -f web

# JSON output for scripting:
godo list -o json | jq '.[] | select(.state=="running") | .name'

# Replace the running daemon with a freshly built godo (kills running jobs).
# Subsequent CLI calls also print a dim notice when the supervisor binary
# doesn't match the CLI build, prompting an upgrade.
godo upgrade

# Shut everything down (no respawn until the next godo invocation):
godo shutdown
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
| `godo [--name X] <cmd> [args...]` | Run `<cmd>` as a supervised job. `--name`/`-n` (before the command) labels the job for `stop`/`rm`/etc. Auto-spawns the daemon. |
| `godo list [-o table\|json]` / `godo ps` | Print all jobs as a table (default) or full JSON records.       |
| `godo stop <id\|name>`           | SIGTERM the job's process group; sets `cancelled`. Sends SIGCONT first so paused jobs unfreeze and receive the signal. |
| `godo restart <id\|name>`        | Stop, wait for actual exit (escalating to SIGKILL after a grace window), then start again with the same spec and hash. |
| `godo pause <id\|name>` / `godo resume <id\|name>` | Freeze a running job (SIGSTOP) / continue a paused one (SIGCONT). The process holds its memory, fds, and ports while paused — handy for "let me check something" without losing state. |
| `godo rm <id\|name>`             | Drop a stopped job from the registry and delete its log dir.           |
| `godo logs <id\|name>`           | Print the job's combined output.                                       |
| `godo logs -f <id\|name>`        | Stream the log: replays existing content then forwards live writes.    |
| `godo attach <id\|name>`         | Take over the job's PTY. Default detach: Ctrl+X then d ($GODO_DETACH overrides). |
| `godo load <file.toml>`          | Import a service file into `~/.godo/services/` and register it.        |
| `godo reload`                    | Rescan `~/.godo/services/`; new files autostart, removed files stop.   |
| `godo monit` / `godo -i`         | Bubble Tea dashboard. j/k move, r restart, p pause/resume, K kill, d remove (confirm with y), Enter attaches a running job or opens the persisted log for a non-running one (j/k/g/G/PgUp/PgDn to scroll, q back). |
| `godo run [flags] -- <cmd>...`   | Explicit form with flags (`--name`, `--restart`, `--nice`, `--env`).   |
| `godo upgrade`                   | Stop the running supervisor so the next CLI call spawns the new binary. Lists running jobs that will die and prompts (`--yes` / `--force`). |
| `godo shutdown`                  | Tell the daemon to stop all children, persist state, and exit.         |
| `godo version`                   | Print the daemon version and build SHA.                                |
| `godo daemon`                    | Run the supervisor in the foreground (debug / dev).                    |

Hidden: `godo supervisor` is the double-fork target invoked by auto-spawn.

## Service files

For long-lived workloads it's nicer to declare them than to remember the `godo run …` command line. Drop a TOML file in `~/.godo/services/` and the daemon picks it up on boot.

```toml
# ~/.godo/services/web.toml
name        = "web"             # optional; defaults to filename without .toml
command     = "node"
args        = ["server.js"]
working_dir = "/srv/web"
autostart   = true              # start on daemon boot / reload
restart     = "on-failure"      # no | on-failure | always
nice        = 5                 # POSIX priority

[env]
PORT = "8080"

[cron]                          # optional
schedule = "0 4 * * *"          # 5-field cron OR @hourly / @daily / @every 5m
overlap  = false                # default: skip a tick if a prior run is still active
```

`godo load /path/to/file.toml` imports a file from anywhere into the services dir (the destination basename is derived from `name`, so source paths from temp dirs / version control are normalized cleanly). `godo reload` rescans the dir and applies the diff:

- New file → registered, autostarted if flagged.
- Removed file → matching job is stopped (kept in the registry as `cancelled` so logs are still readable; `godo rm` to drop).
- Modified file → in-memory spec updated, but **not auto-restarted**. Run `godo restart <name>` when you're ready for the new spec to take effect (matches systemd's `daemon-reload` semantics).

Service identity is the file path. A given file always maps to the same registry entry across daemon restarts, so log dirs and short hashes stay stable.

## Upgrading the binary

The supervisor process is the binary that was running when it first auto-spawned — installing a newer `godo` doesn't replace it in place. To pick up new behavior:

```sh
godo upgrade        # confirms, lists jobs that'll die, shuts the daemon down
```

The next `godo` call auto-spawns the new binary. While the CLI and daemon disagree, every non-management `godo` command prints a dim one-line stderr notice with the two build hashes and a hint to run `godo upgrade`. Both sides identify themselves by `vcs.revision` (with a `+dirty` suffix when the working tree was modified at build time), so locally hacked binaries don't accidentally compare equal to their parent commit.

## Cron

Service files can declare a schedule and the daemon's internal scheduler fires the command on each tick. Each fire creates a fresh registry entry named `<service>@<unix-second>` so multiple runs don't collide. By default `overlap = false` suppresses a new tick if the previous instance is still running — bump to `true` for fan-out workloads.

```toml
# ~/.godo/services/cleanup.toml
name    = "cleanup"
command = "/usr/local/bin/prune-old-logs"

[cron]
schedule = "0 3 * * *"          # 3am every day
```

`@every 30s`, `@hourly`, and `@daily` work too.

## Roadmap

v1 is feature-complete. Things on deck for **v2**, scoped after a dogfooding window:

- **Live job-to-job piping** — `godo pipe A B` fans A's output into B's input. The daemon's output multiplexer and input merger are already shaped for this; v2 just adds the RPC, cycle detection, and a TUI wire-view.
- **Remote / agent-oriented surfaces** — HTTP frontend over the existing wire protocol so the TUI (or a web view, à la zellij) can connect to a daemon on another machine, plus exploration of agent-driven workflows where godo is both the runtime and the introspection surface.
- **Reusable command aliases** — a lightweight `godo alias <name> -- <cmd>...` that survives `godo rm` and lets you re-launch by name (today the closest equivalent is a service file).
- **Ergonomic gaps** — `--quiet` and richer `--output` formats elsewhere, real `ionice` plumbing on Linux, live-upgrade that preserves running jobs across `godo upgrade`.

See `TODO.md` (gitignored) for the running ledger.

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
