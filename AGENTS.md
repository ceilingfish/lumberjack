# Lumberjack

Lumberjack is a Go CLI tool and background daemon that tracks a GitHub repository's open PRs and automatically creates, syncs, and cleans up git worktrees for their branches.

## Architecture: daemon and client

Lumberjack is split into two processes that share no in-memory state:

- **The daemon (`lumberjack daemon`) is the server and sole owner of state.** It owns the SQLite database, runs schema migrations in-process on startup, drives the hourly sync loop, and performs all worktree operations (create/checkout/delete, reconciliation checks). It exposes a gRPC API defined in `proto/lumberjack/v1/`. Nothing else opens the database or shells out to `git` for worktrees — this keeps a single writer and avoids concurrent-access races on both the DB and the working trees.
- **The CLI is a thin gRPC client.** The client does **not** import `internal/database`, `internal/worktree`, or `internal/github` directly. If the daemon is not running, the CLI reports that clearly (and, where appropriate, offers to start it) rather than falling back to direct DB access.

The gRPC contract is the boundary between the two. Design implications:

- **`proto/` is the source of truth.** Change the API by editing the `.proto` files and regenerating; never hand-edit generated code. Run `buf lint` and `buf breaking` before committing changes to the service. Generated stubs are committed so a plain `go build` needs no protoc toolchain.
- **`pkg/client/` is public on purpose.** It holds the buf-generated stubs plus a small hand-written wrapper that handles dialing, connection setup, and turning gRPC status codes into idiomatic Go errors. It lives in `pkg/` (not `internal/`) because it is a genuine reusable API surface — anything that wants to talk to a Lumberjack daemon uses it.
- **The daemon implementation stays in `internal/daemon/`.** The gRPC layer is transport only; business logic stays in the domain packages (`database`, `worktree`, `github`) so it remains unit-testable without a running server.
- **Transport.** The daemon listens on a local endpoint (default a Unix domain socket under `~/.lumberjack/`, path overridable by env var) rather than a public TCP port, since client and server run on the same machine.
- **Lifecycle** uses `github.com/kardianos/service`. The daemon is installed as a **per-user agent** (a LaunchAgent under `~/Library/LaunchAgents` on macOS) so it runs as the invoking user — keeping `~/.lumberjack` paths and the user's `gh auth` credentials valid, with no sudo. It starts at login (`RunAtLoad`) and is restarted if it exits (`KeepAlive`). macOS is the only tested platform for now; other OSes come from the library later. `daemon status` augments the service manager's view with the live PID from `~/.lumberjack/daemon.pid`, which the daemon writes on startup and removes on shutdown.

## Conventions

- **`main.go` stays a one-liner.** It calls `cmd.Execute()` and nothing else. All testable logic lives under `internal/`.
- **`cmd/` files are thin.** Each command parses flags and delegates immediately — CLI commands to the gRPC client in `pkg/client/`, the `daemon` command to `internal/daemon`. No business logic in Cobra's `RunE` handlers, and no direct database or worktree access from `cmd/`.
- **`internal/` by default, `pkg/` only when public.** The compiler forbids importing `internal/`, so we keep freedom to refactor. Promote code to `pkg/` only when we deliberately want it to be a public API.
- **Return errors, don't `os.Exit`** in command logic. Use `RunE` (not `Run`) so Cobra handles exit codes centrally.
- **One file per command** under `cmd/`, mirroring the command hierarchy (`APPNAME VERB NOUN --FLAG`).
- **Start flat, grow modular.** Add `internal/` packages as domains emerge rather than scaffolding everything up front.
- **Non-trivial task logic lives in `scripts/`, not inline in `.mise.toml`.** `mise` tasks should stay one-liners that call a script in `scripts/` (e.g. `scripts/coverage.sh`).
- **Tests live next to the code**, in the same package directory as the code they test.
- **Ship as a single self-contained binary** — no cgo, no C toolchain, no bundled libraries. Everything Lumberjack owns (SQLite engine, migrations) is compiled or embedded into the one executable. The exceptions are **`git` and `gh`, which are required host prerequisites**: Lumberjack shells out to the system `git` for all worktree operations and to the GitHub CLI (`gh`) for GitHub API access and authentication, rather than reimplementing them. Reusing `gh` also lets Lumberjack inherit the user's existing `gh auth login` credentials.

## Shell completion

Cobra generates the completion script; `cmd/completion.go` adds dynamic suggestions (repository names and `gh` logins come from the daemon over gRPC, guarded by a short timeout so a slow or stopped daemon never stalls the shell).

Install it by sourcing the script from your shell rc — the `shell-completion` mise task prints it for the shell you name:

```sh
# ~/.zshrc  (bash/fish/powershell: swap the shell name)
eval "$(mise run shell-completion zsh)"
```

The task runs `go run . completion <shell>`, so it recompiles on each shell start. For a faster startup, generate the file once instead: `lumberjack completion zsh > "${fpath[1]}/_lumberjack"`.

## macOS menu-bar app (`macos/`)

A native Swift app lives under `macos/` — a separate Swift package, built and
distributed independently of the Go binary (see `macos/README.md`). It talks
to the daemon over the same Unix socket the CLI uses, using Swift gRPC/protobuf
stubs generated from `proto/lumberjack/v1/lumberjack.proto` via
`buf generate --template buf.gen.swift.yaml` (parallel to, and never mixed
with, the Go codegen in `buf.gen.yaml`). Regenerate those stubs — and only
those stubs, never hand-edit them — whenever the proto changes.

This is a partial/interim delivery of issue #9: it polls
`ListRepositories`/`ListWorktrees` on a timer instead of consuming the
`Watch` RPC (issue #13), which had not landed when this app was built and
which #9 depends on for its real-time-update acceptance criterion. Do not
treat the polling loop as the intended final design — when #13 lands,
`AppState`'s refresh loop must be replaced with a subscription to that
stream (see `macos/README.md` and comments in `AppState.swift`).
