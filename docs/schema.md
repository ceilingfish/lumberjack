# Lumberjack Database Schema

## Design assumptions

- Lumberjack shells out to the `gh` CLI for all GitHub API access (`gh pr list --json ...`, `gh api ...`). As a result **the database stores no credentials** — no tokens, refresh tokens, or OAuth secrets. Auth (and its refresh) is inherited from `gh`.
- Slug/directory mappings are **stored, not recomputed**, so that changes to the slug rules or ambiguous branch names never orphan an existing worktree.
- **Only state that can't be recovered from git or `gh` is persisted.** PR details (title, state, head SHA, merge commit, fork status) are fetched live from `gh` each sync, and local worktree state (dirty status, tip SHA, whether it's orphaned or needs reconciliation) is read live from git at display time. Neither is cached, so the database can never disagree with reality.

---

## Tables

### `repositories`

One row per tracked repo. This is the identity Lumberjack syncs against.

| Column                | Type      | Notes                                                                                                                    |
| --------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------ |
| `id`                  | integer   | Primary key                                                                                                              |
| `local_path`          | text      | Absolute path to the main checkout; the repo's identity from the user's POV                                              |
| `worktree_parent_dir` | text      | Where sibling worktrees are created. Derived by default, stored so it can be overridden                                  |
| `dir_prefix`          | text      | The `my_repo` component used when building slugs; decoupled from folder name so renaming the folder doesn't break naming |
| `github_owner`        | text      | Parsed from the remote; the API identity                                                                                 |
| `github_name`         | text      | Parsed from the remote                                                                                                   |
| `default_remote`      | text      | Usually `origin`, but not assumed                                                                                        |
| `host`                | text      | `github.com` vs. GitHub Enterprise; needed for the API base URL                                                          |
| `login`               | text      | The `gh` account the repo was registered under (`gh auth status --active`). The daemon switches to it (`gh auth switch`) before any operation and restores the prior account after. Empty for repos tracked before this column existed — no switching |
| `last_synced_at`      | timestamp | For `lumberjack status`                                                                                                  |
| `last_sync_status`    | text      | `ok` / `error`                                                                                                           |
| `last_sync_error`     | text      | Nullable; last error message                                                                                             |
| `etag_pulls`          | text      | ETag from the last PR-list request, for conditional (304) fetches                                                        |
| `created_at`          | timestamp |                                                                                                                          |
| `setup_consent_fingerprint` | text | Content fingerprint (sha256) of the trusted `.lumberjack.yml` run-command steps the local user has consented to run. Empty means not consented. A mismatch against the trusted config's current fingerprint means consent is pending (never given, or the config changed since) |

### `pull_requests`

The PRs the daemon is tracking for a repo. Deliberately minimal — just enough to know which branch each PR maps to and when it was last seen.

| Column           | Type      | Notes                                                             |
| ---------------- | --------- | ----------------------------------------------------------------- |
| `id`             | integer   | Primary key                                                       |
| `repository_id`  | integer   | FK → `repositories.id`                                            |
| `branch_name`    | text      | The PR's head branch, exact/unslugged (e.g. `feature/my-feature`) |
| `last_synced_at` | timestamp | When this sync last observed the PR                               |

Unique constraint on (`repository_id`, `github_pr_number`). Everything else about a PR (title, state, head SHA, merge commit, fork status) is fetched live from `gh` when needed.

### `worktrees`

The local side, and the crucial branch ↔ directory mapping. Holds only what git and `gh` can't reconstruct on their own.

| Column             | Type      | Notes                                                                                                                  |
| ------------------ | --------- | ---------------------------------------------------------------------------------------------------------------------- |
| `id`               | integer   | Primary key                                                                                                            |
| `repository_id`    | integer   | FK → `repositories.id`                                                                                                 |
| `github_pr_number` | integer   | The PR this worktree came from; immutable, so it's safe to persist. Nullable — an orphaned worktree may outlive its PR |
| `branch_name`      | text      | Exact, unslugged                                                                                                       |
| `directory_path`   | text      | The actual resolved path — stored, never recomputed                                                                    |
| `last_synced_at`   | timestamp | When Lumberjack last reconciled this worktree                                                                          |
| `created_at`       | timestamp |                                                                                                                        |
| `setup_error`      | text      | Nullable; names the `.lumberjack.yml` setup step that failed when this worktree was cloned (fail-fast — later steps are skipped, the worktree is kept). Folded into the live reconciliation note/status |

Unique constraint on (`repository_id`, `directory_path`).

`lumberjack worktrees` displays just **directory**, **branch**, and **last synced at** from this table. Reconciliation status (dirty tree, local-only commits, orphaned) is computed live from git when needed, not stored.

### `sync_runs`

Optional but recommended — audit log for `lumberjack status` detail and daemon debugging.

| Column              | Type      | Notes                      |
| ------------------- | --------- | -------------------------- |
| `id`                | integer   | Primary key                |
| `repository_id`     | integer   | FK → `repositories.id`     |
| `started_at`        | timestamp |                            |
| `finished_at`       | timestamp | Nullable while in progress |
| `status`            | text      | `ok` / `error`             |
| `worktrees_created` | integer   |                            |
| `worktrees_removed` | integer   |                            |
| `error`             | text      | Nullable                   |

---

## Why these fields matter for the GitHub sync loop

- **`etag_pulls`** — the "hourly, cheap, incremental" polling story depends on conditional requests to stay under rate limits.
- **`github_pr_number`** — immutable link back to the source PR; lets a worktree be re-associated even after slug rules change, without caching mutable PR state.
- **`directory_path` stored, not computed** — makes the reverse (directory → branch) lookup reliable and immune to slug-rule changes.

The daemon manages every tracked worktree uniformly, regardless of whether it created the worktree or adopted one already on disk. A worktree whose PR has merged or closed is removed only when it is provably safe to do so — a clean tree with no local-only commits; anything dirty or holding un-pushed work is retained (see "Derived live, not stored").

## Derived live, not stored

Fetched fresh each sync rather than cached, so the database can't go stale:

- **PR state** (title, `open`/`closed`/`merged`, head SHA, merge commit SHA, base ref, fork owner) — from `gh pr list --json ...` / `gh pr view`. Merged-vs-closed and squash-merge content checks are done against this live data.
- **Local worktree state** (uncommitted changes, local tip SHA, whether a branch is orphaned or needs reconciliation) — from `git status` / `git rev-parse` at the moment it's needed.

## Not stored

Because Lumberjack shells out to `gh`, there is **no `credentials` table**. If Lumberjack ever moves to direct GitHub API calls, credentials should live in the OS keychain with only a reference stored here — not in the database directly.
