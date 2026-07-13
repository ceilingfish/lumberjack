# Lumberjack

Lumberjack is a Go CLI tool and background daemon that tracks a GitHub repository's open PRs and automatically creates, syncs, and cleans up git worktrees for their branches.

## Repo layout

Lumberjack follows the standard Go project layout, built on top of a [Cobra](https://github.com/spf13/cobra) CLI scaffold.

```
lumberjack/
├── main.go            # Thin entry point — only calls cmd.Execute()
├── cmd/               # Cobra command definitions, one file per command
│   ├── root.go        # Root command + shared flag wiring
│   ├── init.go        # `lumberjack init .`
│   ├── repositories.go
│   ├── repository.go
│   ├── sync.go
│   ├── daemon.go      # `lumberjack daemon` — run the gRPC server
│   └── doctor.go      # `lumberjack doctor` — check git/gh prerequisites
├── proto/             # Protobuf service + message definitions (source of truth)
│   └── lumberjack/v1/ # Versioned package; `lumberjack.v1` service
├── internal/          # Private application logic (not importable externally)
│   ├── config/        # Config loading and persistence
│   ├── database/      # Database access layer (daemon-only)
│   │   ├── schema/    # Schema / models for tracked repos
│   │   └── migrations/ # goose migration files, embedded via embed.FS
│   ├── github/        # GitHub API auth + PR status queries
│   ├── worktree/      # Worktree create/checkout/delete + name resolution
│   └── daemon/        # gRPC server + hourly background sync loop
│       └── server.go  # Implements the generated LumberjackServiceServer
├── pkg/               # Public packages (only if we intend external reuse)
│   └── client/        # Generated gRPC stubs + a thin hand-written client wrapper
├── scripts/           # Helper shell scripts invoked by mise tasks
│   └── coverage.sh
└── go.mod
```

### Frameworks

- **[Go](https://go.dev)** — implementation language and toolchain.
- **[mise-en-place](https://mise.jdx.dev)** (`mise`) — task runner and tool/version management.
- **[Cobra](https://github.com/spf13/cobra)** — CLI framework for commands, flags, and help.
- **[gofumpt](https://github.com/mvdan/gofumpt)** — formatter; a stricter, opinionated superset of `gofmt`.
- **[golangci-lint](https://golangci-lint.run)** — meta-linter aggregating `staticcheck`, `govet`, `errcheck`, `revive`, and others behind one config; as of v2 it also runs the formatters.
- **[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)** — pure-Go SQLite driver (no cgo), keeping the binary self-contained.
- **[goose](https://github.com/pressly/goose)** — schema migrations, embedded via `embed.FS` and applied in-process at runtime.
- **[sqlfluff](https://sqlfluff.com/)** — dialect-aware SQL linter and formatter for the goose migration files under `internal/database/migrations/`, configured for the `sqlite` dialect via `.sqlfluff`.
- **[gRPC](https://grpc.io) + [Protocol Buffers](https://protobuf.dev)** — the wire protocol between the CLI (client) and the daemon (server). Service and message definitions live in `proto/` as the single source of truth.
- **[buf](https://buf.build)** — protobuf toolchain used for linting, breaking-change detection, and code generation (`buf generate`). Generated Go stubs (`*.pb.go`, `*_grpc.pb.go`) are committed to the repo so a plain `go build` needs no protoc toolchain.

### Architecture: daemon and client

Lumberjack is split into two processes that share no in-memory state:

- **The daemon (`lumberjack daemon`) is the server and sole owner of state.** It owns the SQLite database, runs schema migrations in-process on startup, drives the hourly sync loop, and performs all worktree operations (create/checkout/delete, reconciliation checks). It exposes a gRPC API defined in `proto/lumberjack/v1/`. Nothing else opens the database or shells out to `git` for worktrees — this keeps a single writer and avoids concurrent-access races on both the DB and the working trees.
- **The CLI is a thin gRPC client.** Commands under `cmd/` parse flags and call into the generated client in `pkg/client/`; they do **not** import `internal/database`, `internal/worktree`, or `internal/github` directly. If the daemon is not running, the CLI reports that clearly (and, where appropriate, offers to start it) rather than falling back to direct DB access.

The gRPC contract is the boundary between the two. Design implications:

- **`proto/` is the source of truth.** Change the API by editing the `.proto` files and regenerating; never hand-edit generated code. Run `buf lint` and `buf breaking` before committing changes to the service.
- **`pkg/client/` is public on purpose.** It holds the buf-generated stubs plus a small hand-written wrapper that handles dialing, connection setup, and turning gRPC status codes into idiomatic Go errors. It lives in `pkg/` (not `internal/`) because it is a genuine reusable API surface — anything that wants to talk to a Lumberjack daemon uses it.
- **The daemon implementation stays in `internal/daemon/`.** `server.go` implements the generated `LumberjackServiceServer` interface and delegates to the existing `internal/` domain packages (`database`, `worktree`, `github`). The gRPC layer is transport only; business logic stays in the domain packages so it remains unit-testable without a running server.
- **Transport.** The daemon listens on a local endpoint (default a Unix domain socket under `~/.lumberjack/`, path overridable by env var) rather than a public TCP port, since client and server run on the same machine.

### Conventions

- **`main.go` stays a one-liner.** It calls `cmd.Execute()` and nothing else. All testable logic lives under `internal/`.
- **`cmd/` files are thin.** Each command parses flags and delegates immediately — CLI commands to the gRPC client in `pkg/client/`, the `daemon` command to `internal/daemon`. No business logic in Cobra's `RunE` handlers, and no direct database or worktree access from `cmd/`.
- **`internal/` by default, `pkg/` only when public.** The compiler forbids importing `internal/`, so we keep freedom to refactor. Promote code to `pkg/` only when we deliberately want it to be a public API.
- **Return errors, don't `os.Exit`** in command logic. Use `RunE` (not `Run`) so Cobra handles exit codes centrally.
- **One file per command** under `cmd/`, mirroring the command hierarchy (`APPNAME VERB NOUN --FLAG`).
- **Start flat, grow modular.** Add `internal/` packages as domains emerge rather than scaffolding everything up front.
- **Non-trivial task logic lives in `scripts/`, not inline in `.mise.toml`.** `mise` tasks should stay one-liners that call a script in `scripts/` (e.g. `scripts/coverage.sh`). Keep anything beyond a single command out of the TOML.
- **Tests live next to the code**, not in a separate directory. A `_test.go` file sits in the same package directory as the code it tests (e.g. `internal/worktree/worktree_test.go`).
- **Ship as a single self-contained binary.** The distributable must have no *bundled* dependencies — no cgo, no C toolchain, no libraries to install alongside it. Everything Lumberjack owns (SQLite engine, migrations) is compiled or embedded into the one executable. The exceptions are **`git` and `gh`, which are required host prerequisites**: Lumberjack shells out to the system `git` for all worktree operations and to the GitHub CLI (`gh`) for GitHub API access and authentication, rather than reimplementing them. Both are reliably present on any machine doing PR-based development, and reusing `gh` lets Lumberjack inherit the user's existing `gh auth login` credentials.
