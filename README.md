# Lumberjack

Lumberjack tracks a GitHub repository's open pull requests and automatically
creates, syncs, and cleans up a git **worktree** for each PR branch. Point it at
a repository once and a background daemon keeps a working tree on disk for every
open PR — created when the PR opens, removed when it closes — so you can jump
between branches without stashing, re-checking-out, or clobbering your main
checkout.

It runs as two pieces:

- a **daemon** — a background service that owns the state, runs an hourly sync,
  and performs every git worktree operation;
- a **CLI** (`lumberjack`) — the command you use, a thin client that talks to the
  daemon.

---

## Prerequisites

Lumberjack shells out to two host tools rather than reimplementing them, so both
must be installed and on your `PATH`:

| Tool | Why | Check |
| --- | --- | --- |
| **[git](https://git-scm.com)** | all worktree operations | `git --version` |
| **[GitHub CLI (`gh`)](https://cli.github.com)** — **authenticated** | GitHub API access; Lumberjack reuses your `gh` login | `gh auth status` |

Authenticate `gh` before using Lumberjack:

```sh
gh auth login
```

**Platform:** macOS is the only tested platform today. The daemon installs as a
per-user LaunchAgent (no `sudo`). Other operating systems may work but are
unsupported for now.

**To build from source** you also need [Go](https://go.dev) 1.26+. The repo uses
[mise](https://mise.jdx.dev) to pin the toolchain and provide tasks; installing
mise is optional but recommended.

Verify your prerequisites at any time with:

```sh
lumberjack doctor
```

It reports where `git` and `gh` are, their versions, and whether `gh` is
authenticated — and exits non-zero if anything is missing, so it's safe to use
in scripts. `doctor` does **not** need the daemon running.

---

## Installation

### With Go

```sh
go install github.com/ceilingfish/lumberjack@latest
```

This puts a `lumberjack` binary in `$(go env GOPATH)/bin` — make sure that's on
your `PATH`.

### From source (with mise)

```sh
git clone https://github.com/ceilingfish/lumberjack.git
cd lumberjack
mise run build          # produces ./bin/lumberjack
```

Copy `bin/lumberjack` somewhere on your `PATH` (e.g. `~/.local/bin`).

---

## Running from source

For day-to-day development there are two ways to run the daemon, and it matters
which you use.

**Foreground (development):** run the daemon in the foreground, tied to your
terminal, with logs on stdout. Stop it with Ctrl-C. Nothing is registered with
launchd.

```sh
mise run dev            # = `go run . daemon run`
```

**Installed service:** to register the daemon so it starts at login, install
from a **built binary**, never from `go run`. The `install` task builds one and
installs it in a single step:

```sh
mise run install        # go build -o bin/lumberjack . && daemon install
./bin/lumberjack daemon start
```

> **Why not `go run . daemon install`?** `go run` links the program to a
> **transient, content-addressed path** under a `go-build` directory — the hash
> changes every time you rebuild, and Go prunes the build cache over time. So the
> path is not a stable install target: register the service against it and the
> next build leaves launchd pointing at a binary that has moved or been pruned.
> The daemon then fails to launch and, under `KeepAlive`, crash-loops; a later
> `daemon start` fails with `launchctl … Load failed: 5`. `daemon install`
> refuses to run from a `go run` build for exactly this reason and points you
> back here.

After rebuilding, upgrade the installed service in place with `--force`, which
stops and removes the old registration then reinstalls with the new binary path
and environment:

```sh
mise run install -- --force
./bin/lumberjack daemon start
```

---

## Setup

### 1. Install and start the daemon

```sh
lumberjack daemon install   # register it to start automatically at login
lumberjack daemon start     # start it now
lumberjack daemon status    # confirm it's running
```

`install` writes a per-user LaunchAgent (`~/Library/LaunchAgents` on macOS) that
starts at login and restarts if it exits. You only do this once.

### 2. Track a repository

From inside a GitHub repository checkout:

```sh
cd ~/code/my-project
lumberjack init
```

`init` registers the repository at the current directory (pass a path to point
elsewhere: `lumberjack init ~/code/other`). The daemon then tracks its open PRs
and, on each sync, reconciles worktrees for their branches. It prints where the
worktrees will be created.

### 3. Let it run — or sync on demand

The daemon syncs automatically every hour. To reconcile immediately:

```sh
lumberjack sync             # sync the repo in the current directory
lumberjack status           # show its last-sync detail and worktrees
```

That's the whole setup. New PRs get worktrees; closed PRs get theirs cleaned up.

---

## Everyday commands

Run `lumberjack <command> --help` for full details on any of these.

| Command | Does |
| --- | --- |
| `lumberjack doctor` | Check `git`/`gh` prerequisites (no daemon needed). |
| `lumberjack init [path]` | Start tracking the repository at `path` (default: current dir). |
| `lumberjack delete NAME` | Stop tracking a repository — removes it and its worktrees from the database only (nothing on disk or GitHub). |
| `lumberjack status` | Last-sync detail for the repo in the current dir. |
| `lumberjack sync` | Reconcile worktrees for the repo in the current dir now. |
| `lumberjack repositories` | List every tracked repository. |
| `lumberjack repositories --sync` | Sync **all** tracked repositories. |
| `lumberjack repositories NAME` | Show one repository's detail. |
| `lumberjack repositories NAME worktrees` | List that repo's worktrees. |
| `lumberjack repositories NAME sync` | Sync just that repository. |
| `lumberjack repositories NAME worktree BRANCH_OR_DIR delete` | Delete a worktree (prompts if it would lose commits; `--force` to skip). |
| `lumberjack set-login [LOGIN]` | Set the `gh` account for the repo in the current dir. |
| `lumberjack repositories NAME set-login [LOGIN]` | Same, targeting a repo by name. |
| `lumberjack daemon install/start/stop/status` | Manage the daemon's lifecycle. |

`NAME` resolves against a repository's name or its local path.

### Using a specific GitHub account

If you're signed in to `gh` with more than one account, tell Lumberjack which one
a repository should operate under:

```sh
lumberjack set-login                 # pick interactively from your gh accounts
lumberjack set-login my-work-login   # or name one directly
```

---

## Shell completion

Lumberjack ships completion for common shells, with live suggestions for
repository names and `gh` logins. Add this to your shell's rc file (swap `zsh`
for `bash`, `fish`, or `powershell`):

```sh
# ~/.zshrc
eval "$(mise run shell-completion zsh)"
```

From source without mise, or for a faster shell startup, generate the script once
instead of on every launch:

```sh
lumberjack completion zsh > "${fpath[1]}/_lumberjack"
```

---

## Where Lumberjack keeps things

- **State & socket:** `~/.lumberjack/` (SQLite database, the daemon's Unix
  socket, and its PID file). Override the socket path with the
  `LUMBERJACK_SOCKET_PATH` environment variable.
- **Worktrees:** created under a per-repository parent directory that `init`
  reports; each is named from the repo and branch.

---

## Managing the daemon

```sh
lumberjack daemon status    # is it running? shows version and live PID
lumberjack daemon stop      # stop it
lumberjack daemon start     # start it again
```

If the daemon isn't running, CLI commands that need it will say so clearly rather
than fall back to touching your repositories directly.
