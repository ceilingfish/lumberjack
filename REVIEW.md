# Review checklist

Before a change is considered done, work through the following. All checks must
pass.

## 1. Enforce the repository structure

Verify the change conforms to the repository structure and conventions defined
in [AGENTS.md](AGENTS.md). In particular:

- New files live in the correct package under `internal/` (or `pkg/` only if
  deliberately public).
- `main.go` stays a one-liner and `cmd/` files stay thin — no business logic in
  Cobra `RunE` handlers.
- One file per command under `cmd/`, mirroring the command hierarchy.
- Non-trivial task logic lives in `scripts/`, not inline in `.mise.toml`.
- Tests live next to the code they test (`_test.go` in the same package).

## 2. Format & lint

Run the formatter and linter, and resolve any issues:

```sh
mise run format   # gofumpt -w .
mise run lint      # golangci-lint run
```

## 3. Tests

Run the test suite and ensure **all tests pass**:

```sh
mise run test      # go test ./...
```

## 4. Coverage

Run the coverage task and ensure all new code meets the coverage standard (total
statement coverage must not fall below the 80% threshold):

```sh
mise run coverage  # scripts/coverage.sh 80
```
