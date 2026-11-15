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
- Task logic lives in a mise file task under `mise-tasks/`, not inline in `.mise.toml`.
- Tests live next to the code they test (`_test.go` in the same package).

## 2. Format & lint

The two codebases are formatted and linted separately — `cli:format` for the Go
CLI/daemon (which also covers the SQL migrations and protobuf), `osx:format` for
the Swift menu-bar app. Each formats in place and then lints; pass `--check` to
fail on a diff without writing, as CI does. Run whichever the branch touches,
and resolve any issues:

```sh
mise run cli:format            # Go, SQL migrations, protobuf
mise run cli:format --check    # what CI runs, including buf breaking against main

mise run osx:format            # Swift, under macos/
```

If the current branch changes any protobuf files, also regenerate the stubs:

```sh
mise run proto:generate-clients   # commit the regenerated Go and Swift stubs
```

**All protobuf changes must be backward compatible.** The gRPC API is the
contract between the CLI and the daemon, which may be running different
versions, so `mise run proto:format --check` must pass — it checks the proto
against `main` and fails on any wire-incompatible change. Evolve the API
additively: add new fields, messages, and RPCs; never renumber or reuse field
tags, change a field's type, or remove/rename existing fields (reserve retired
tag numbers instead). Regenerated stubs under `pkg/client/` must be committed
alongside the `.proto` change.

## 3. Tests

Run the test suite and ensure **all tests pass**:

```sh
mise run cli:test    # Go
mise run osx:test    # Swift, under macos/
```

## 4. Coverage

Run the coverage tasks and ensure all new code meets the coverage standard. The check must pass:

```sh
mise run cli:coverage
mise run osx:coverage
mise run coverage:ci-check
```

**The floor is per package, not a global average**, so a well-covered package
cannot mask an untested one. Every package must independently meet the
threshold; the run lists every package below it, not just the first. A package
containing code but no test files **fails** — packages with no tests are absent
from Go's coverage profile entirely, and that absence is exactly what the gate
exists to catch.

It covers both codebases, with the Swift app treated as a package
(`macos/Sources/LumberjackMenuBar`) alongside the Go ones. Both halves reach
the gate as lcov, produced by `cli:coverage` and `osx:coverage`; the per-language
subtotals and merged total the run prints are information only, not the gate.

The threshold is the gate's `-Threshold` argument, so it can be ratcheted
upward as packages are brought up to standard:

```sh
pwsh -NoProfile -File scripts/coverage-gate.ps1 -Threshold 95   # what `mise run coverage:ci-check` runs today
```

It sits below the 95% target, and each coverage child of issue #6 raises it as
its package lands. **The floor currently fails on `main`**, and that is
deliberate: releases are gated on the tests passing, not on the floor being
met, so the `coverage` job in
[the release workflow](.github/workflows/release.yml) reports the gate without
blocking `release-please`. Do not lower the threshold to make a change pass;
raise the coverage of what you touched instead.

Files listed in [`coverage-exclude.txt`](coverage-exclude.txt) count toward
neither numerator nor denominator. Add an entry only with a comment justifying
it, and prefer making code testable over exempting it. A leading `/` anchors a
pattern to the repository root, a pattern with no `/` matches a base name at
any depth, and `**` matches zero or more path segments.

**The PowerShell tooling itself is not measured for coverage**: only the Go
and Swift halves are gated, and no `.ps1` file should appear as a package or in
`coverage.lcov`.

## 5. Stylistic review

Once all the checks above pass, carry out the stylistic review using both
skills:

- `/go-code-review` — check the change against Go community style standards.
- `/ponytail-review` — hunt for over-engineering and anything that can be
  deleted or simplified.
