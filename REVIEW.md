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
mise run format
mise run lint
```

If the current branch changes any SQL files, also run the SQL formatter and
linter and resolve any issues:

```sh
if git diff --name-only main...HEAD | grep -q '\.sql$'; then
  mise run format-sql
  mise run lint-sql
fi
```

If the current branch changes any protobuf files, also run the protobuf
formatter, linter, breaking-change check, and regenerate the stubs, resolving
any issues:

```sh
if git diff --name-only main...HEAD | grep -q '\.proto$'; then
  mise run format-proto
  mise run lint-proto
  mise run lint-proto-breaking
  mise run generate   # commit the regenerated stubs under pkg/client/
fi
```

**All protobuf changes must be backward compatible.** The gRPC API is the
contract between the CLI and the daemon, which may be running different
versions, so `mise run lint-proto-breaking` must pass — it checks the proto
against `main` and fails on any wire-incompatible change. Evolve the API
additively: add new fields, messages, and RPCs; never renumber or reuse field
tags, change a field's type, or remove/rename existing fields (reserve retired
tag numbers instead). Regenerated stubs under `pkg/client/` must be committed
alongside the `.proto` change.

## 3. Tests

Run the test suite and ensure **all tests pass**:

```sh
mise run test
```

## 4. Coverage

Run the coverage task and ensure all new code meets the coverage standard. Command must pass and ensure that the coverage requirements are met:

```sh
mise run coverage
```
