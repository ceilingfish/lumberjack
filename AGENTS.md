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
│   └── sync.go
├── internal/          # Private application logic (not importable externally)
│   ├── config/        # Config loading and persistence
│   ├── schema/        # Database schema / models for tracked repos
│   ├── github/        # GitHub API auth + PR status queries
│   ├── worktree/      # Worktree create/checkout/delete + name resolution
│   └── daemon/        # Hourly background sync process
├── pkg/               # Public packages (only if we intend external reuse)
└── go.mod
```

### Frameworks

- **[Go](https://go.dev)** — implementation language and toolchain.
- **[mise-en-place](https://mise.jdx.dev)** (`mise`) — task runner and tool/version management.
- **[Cobra](https://github.com/spf13/cobra)** — CLI framework for commands, flags, and help.

### Conventions

- **`main.go` stays a one-liner.** It calls `cmd.Execute()` and nothing else. All testable logic lives under `internal/`.
- **`cmd/` files are thin.** Each command parses flags and delegates immediately to a function in `internal/`. No business logic in Cobra's `RunE` handlers.
- **`internal/` by default, `pkg/` only when public.** The compiler forbids importing `internal/`, so we keep freedom to refactor. Promote code to `pkg/` only when we deliberately want it to be a public API.
- **Return errors, don't `os.Exit`** in command logic. Use `RunE` (not `Run`) so Cobra handles exit codes centrally.
- **One file per command** under `cmd/`, mirroring the command hierarchy (`APPNAME VERB NOUN --FLAG`).
- **Start flat, grow modular.** Add `internal/` packages as domains emerge rather than scaffolding everything up front.
- **Tests live next to the code**, not in a separate directory. A `_test.go` file sits in the same package directory as the code it tests (e.g. `internal/worktree/worktree_test.go`).
