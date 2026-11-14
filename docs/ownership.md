# PR Authorship and Mergeability

Status: design. Nothing here is implemented yet.

This spec introduces a first-class distinction between pull requests **the user
authored** and pull requests **other people authored that we sync**, and shows
how far along each one is. It extends `docs/schema.md`; where the two disagree,
this document is the newer intent.

Automating the user's own PRs, which builds on this classification, is tracked
separately in issue #110.

## Why authorship is the foundation

Today every tracked worktree is treated identically: sync creates one per open
PR, reconciles it, and removes it when the PR leaves the open set. That is
correct for the mechanics of worktree management and wrong for everything we
want to do next, because the two kinds of change have opposite goals:

- A **`mine`** change is work in progress. The goal is to show the user what is
  still stopping it from merging: unaddressed review feedback, failing checks,
  conflicts.
- A **`theirs`** change is someone else's work awaiting the user's attention.
  The goal is to tell the user which one to look at first, and to notice when it
  changes under them. The user wants Lumberjack to _triage it_.

## Determining authorship

Every worktree has an **authorship**, which is exactly one of two values:

| value    | meaning                                                    |
| -------- | ---------------------------------------------------------- |
| `mine`   | the user's own change                                      |
| `theirs` | someone else's change, synced in for the user to look at  |

There is no third, unclassified state. **Whenever the evidence does not prove a
worktree is `mine`, it is `theirs`.** A wrong `theirs` is cheap — the PR shows on
the Incoming tab — and the user corrects it with `lumberjack set-authorship`
(see "Manual override").

These two names are used everywhere: in Go (a typed `Authorship` string with one
constant per value), in the proto (`AUTHORSHIP_MINE`, `AUTHORSHIP_THEIRS`), and
in every database column that holds an authorship, which has a `CHECK`
constraint limiting it to those values. The verdict itself is derived rather
than stored (see "Why the verdict is not a column"); the manual override and the
three conditions below are its inputs.

### 1. The user created it locally

`lumberjack worktree add` is an unambiguous statement of intent: the user asked for this branch, on this machine. The worktree is recorded with `origin = 'local'`, which makes its authorship
`mine` until a PR exists (see "Resolution order").

### 2. The PR is authored by the logged-in account

When sync finds a PR, its author decides authorship: `mine` if the author is one
of the user's identities (see "Identity sets"), otherwise `theirs`.

Once a PR exists, its author is the authoritative statement of whose change it
is, and it is immutable for the life of the PR — the same property that makes
`github_pr_number` safe to persist on `worktrees`. So `pull_requests` gains
`author_login`, populated from the sync fetch. Because `pull_requests` rows are
soft-deleted when a PR closes (see "Soft-deleted pull requests"), the author is
still known for an orphaned worktree whose PR has merged or closed.

`github.PR` gains `AuthorLogin`, and `ListOpenPRs` adds `author` to its
`--json` selection (`gh` returns `author.login`). `FindPRForBranch` adds the
same, so a PR discovered after the fact between sync ticks is classified too.

The comparison is **derived at read time, not stored**: both
operands are already in the database, and persisting the verdict would create a
third copy of a fact that goes stale the moment the identity set changes.

### 3. Every commit on the branch is the user's

This covers the case the other two miss: a branch pushed and adopted, with no
PR yet. If the only commits between the default branch and the branch tip were
authored by the user, it is their work.

**Do not compute the email → account association locally.** GitHub already
performs it, against the full set of verified addresses on an account plus the
`users.noreply` forms, and neither of those is knowable from the client. Every
commit object the API returns carries both the raw git trailer and the resolved
account:

```
$ gh api "repos/ceilingfish/lumberjack/compare/0f1d30f...main" \
    --jq '[.commits[] | {e: .commit.author.email, login: (.author.login // "UNRESOLVED")}]'
[{"e":"ceilingfish@gmail.com","login":"ceilingfish"}, ...]
```

Three properties of this data decide the design:

- **Key on `author`, never `committer`.** On this repository's own `main` the
  committer resolves to `web-flow` — GitHub's web-UI merge bot, email
  `noreply@github.com` — because the commits were squash-merged through the web
  UI. Committer identifies whoever applied the commit; author identifies who
  wrote it, which is the question authorship asks.
- **`author` may be null.** An address belonging to no GitHub account resolves
  to nothing. A null author is not proof the commit is the user's, so the
  branch is `theirs`.
- **`compare/BASE...HEAD` is the right endpoint.** It returns exactly the
  commits between the merge base and the head in a single call, already
  resolved. `commits?sha=<branch>` walks the entire history and forces the
  caller to paginate until it finds the divergence point.

So condition 3 resolves in layers, and needs no local identity matching at all:

| branch state  | source                                                                                                       |
| ------------- | ------------------------------------------------------------------------------------------------------------ |
| pushed, no PR | `gh api repos/O/N/compare/<default>...<branch>`                                                              |
| never pushed  | GitHub knows nothing — and a branch that exists only locally is the user's by construction. `mine`, no lookup |

The branch is `mine` only when every commit in the range resolves to a login in
the identity set. Everything else is `theirs`:

- any commit by a login outside the identity set;
- any commit whose author is null;
- an empty range (branch tip equals the merge base) — a freshly adopted branch
  with no commits of its own tells us nothing;
- a range `compare` truncates. It stops at 250 commits, and a branch that far
  from the default branch means something has gone badly wrong, so it is not
  worth paginating to find out.

The default branch is the comparison point here only because a branch with no
PR has no base. Once a PR exists, condition 2 decides and this comparison is
never made.

Author resolution is keyed on an immutable commit SHA, so it is cacheable
indefinitely rather than per-tick. The API only knows commits that have been
pushed, which is why an unpushed branch is handled separately above.

### Identity sets

A single `repositories.login` comparison is **not sufficient**, and this
repository is itself the counterexample. It is registered under login
`ceilingfish`, its local `user.email` is `tom.parrish@parall.ax` resolving to
`ceilingfish-parallax`, and both accounts author commits on it. Under a
single-login check the user's own PRs classify as `theirs`.

One human routinely has several GitHub accounts — personal and work, or one per
client — and `gh` already models this: it authenticates several accounts per
host, and `ListLogins` already returns them. So authorship compares the PR author
against an **identity set**:

1. `repositories.login`, the account the repo was registered under.
2. Every other login `gh` is authenticated as for that host (`gh auth status`),
   which is already available via `github.Client.ListLogins`.

Comparison is case-insensitive; GitHub logins are not case-sensitive. Source 2
means the common multi-account case needs no configuration at all. There is no
list of extra identities to maintain: a PR by an account `gh` does not hold — a
shared bot, or a second machine's account — is `theirs`, and the user corrects
the worktrees that matter with `set-authorship`.

### Manual override

When the derived answer is wrong, the user sets it by hand:

```
lumberjack set-authorship [--repository NAME] [--worktree BRANCH_OR_DIRECTORY_NAME] mine|theirs|auto
```

`--repository` resolves like every other scoped command. `--worktree` resolves
the way `worktree delete` resolves its argument; without it, the command acts on
the tracked worktree containing the current directory, and neither present is a
clear error. The command calls the `SetAuthorship` RPC, which records the value
in `worktrees.authorship_override`.

The override is per worktree and permanent: it wins over every condition below,
including a later PR, until the user sets it again. It is the user's explicit
statement, and nothing the daemon learns afterwards is better evidence than
that. `auto` clears it, setting `authorship_override` back to null so the
conditions decide again.

### Resolution order

```
if authorship_override is set:     the override, alone
else if the worktree has a PR:     condition 2 decides, alone
else if origin == 'local':         mine
else:                              condition 3 decides
```

"Has a PR" means `worktrees.github_pr_number` is set and a `pull_requests` row
exists for it, soft-deleted or not. A row whose `author_login` is still empty
(a PR not yet seen by a sync since the migration) is `theirs` until the next
sync fills it in.

Condition 2 is deliberately exclusive rather than an `OR` with conditions 1
and 3. If the user created a worktree locally and somebody else opened the PR on
that branch, the PR author is right and the local provenance is stale. An `OR`
would let condition 1 permanently out-vote reality.

The result is always `mine` or `theirs`; every path that cannot prove `mine`
ends at `theirs`.

## Mergeability state

Authorship says which tab a PR is in; mergeability says how far along it is.
All of it is mutable and all of it is reconstructible from GitHub, so per
`docs/schema.md` **none of it is written to the database**. It is fetched on
each refresh (see "Refreshing the rollup"), held in memory, and folded into the
worktree view alongside the existing reconciliation status.

The fields used:

| field               | use                                                                                  |
| ------------------- | ------------------------------------------------------------------------------------ |
| `mergeStateStatus`  | `CLEAN`, `BLOCKED`, `BEHIND`, `DIRTY`, `UNSTABLE`, ... `DIRTY` means a merge conflict |
| `reviewDecision`    | `APPROVED` / `CHANGES_REQUESTED` / `REVIEW_REQUIRED`                                 |
| `statusCheckRollup` | per-check conclusions and `isRequired` — the static analysis signal                  |
| `isDraft`           | shown on the row, and lowers the `mine` score                                        |

**Conflicts are always against the PR's base branch, never the repository's
default branch.** A stacked PR whose base is another feature branch conflicts
or not against that feature branch; what it would do against `main` is
irrelevant until it is retargeted. GitHub computes `mergeStateStatus` against
the PR's `baseRefName`, and a conflict is `mergeStateStatus == DIRTY`. The
separate `mergeable` field is not fetched; `mergeStateStatus` carries the same
conflict signal and also says why a clean PR still cannot merge (`BEHIND`,
`BLOCKED`, `UNSTABLE`), so one field answers both questions.

This is the same surface issue #100 proposes as `MergeState`, and it should be
one message rather than two overlapping ones. #100's `base_commit_sha` and
`conflict_count` describe a _local_ merge attempt; the fields above describe
GitHub's view. Both belong on the worktree view, and the naming should make
clear which is which — suggest `MergeState` for the local attempt (as #100 has
it) and `PullRequestState` for the GitHub-side rollup. The local attempt follows
the same rule: it merges the PR's base branch (`baseRefName`, fetched with the
rollup), not the default branch, so the two views can never disagree about
which branch a conflict is against.

### Failing checks

**Only required checks count, and only when they would block the merge.** The
aim is to mirror GitHub's own rule for when a check stops a PR merging, so the
badge never flags a check GitHub would let the user merge past.

- A check is **required** when GitHub says so for this PR: the `isRequired`
  field on each `CheckRun` and `StatusContext` in `statusCheckRollup`, which
  GitHub evaluates against the branch protection and rulesets of the PR's base
  branch. Non-required checks are ignored entirely, whatever their result.
- A required check is **failing** when its conclusion is `FAILURE`,
  `CANCELLED`, `TIMED_OUT`, `ACTION_REQUIRED` or `STARTUP_FAILURE` (check runs),
  or its state is `FAILURE` or `ERROR` (commit statuses).
- `SKIPPED`, `NEUTRAL` and `SUCCESS` are passing, as they are to GitHub.
- A required check that is queued, in progress, or has not reported yet is
  pending. Pending is not failing: it is shown, but never counted as a problem.

`ChecksSummary.state` is `FAILING` if any required check is failing, else
`PENDING` if any is pending, else `PASSING`. A PR with no required checks —
including every PR in a repository without branch protection — is
`CHECK_STATE_UNSPECIFIED` and never shows a check problem. The counts in
`ChecksSummary` cover required checks only.

`isRequired` takes the PR number as an argument, which a single
`repository.pullRequests` connection cannot supply per node. The fetch is
therefore two steps: list the open PR numbers (which sync already does), then
send one GraphQL query that aliases each PR — `pr123: pullRequest(number: 123)`
— so each alias can pass its own number to `isRequired`. That is still one
request per repository, not one per PR.

`GetRepository`, `ListWorktrees` and `Watch` all carry the worktree view, so
all three gain it at once. `Authorship` is an enum on the same message. See
"gRPC surface" below for the wire shape.

Cost is the open problem: issue #101 already reports sync scaling badly with
worktree count, so the rollup comes from a single batched GraphQL query over the
repository's open PRs (`gh api graphql`), not one `gh pr view` per PR.

### Refreshing the rollup

The rollup — everything behind the badges, dots, scores and row details — is
refreshed from GitHub in exactly three circumstances, and never otherwise:

1. **Daemon start**, for every tracked repository. The daemon already runs a
   sync of all repositories straight away on start, and the rollup fetch is part
   of it.
2. **The `Sync` RPC**, for the repository it names, whoever calls it: the CLI
   (`lumberjack sync`, `lumberjack sync-all`) or the macOS app's sync button.
3. **The hourly sync loop** in the daemon, for every tracked repository.

The rollup fetch is part of a repository's sync, so after any of the three
`Repository.last_synced_at` and the rollup describe the same moment. Read RPCs
(`ListWorktrees`, `GetRepository`, `Watch`) never trigger a fetch.

## gRPC surface

The macOS app shows one repository at a time, chosen from a dropdown. It splits
that repository's PRs into two tabs, `mine` and `theirs`, the latter labelled
"Incoming", and orders each tab by how much it needs the user. Every worktree
appears in exactly one tab. The wire changes are additive fields on `Worktree`
and two new RPCs.
Because every field and RPC is new, `buf breaking` passes and an
already-released app ignores them.

### What the views need

| need                        | source                                                                                       | `mine` | `theirs` |
| --------------------------- | -------------------------------------------------------------------------------------------- | ------ | -------- |
| which tab a PR belongs in   | derived authorship (see "Resolution order")                                                  | ✓      | ✓        |
| unresolved comments         | GraphQL `reviewThreads { isResolved }`                                                       | ✓      | ✓        |
| blocking reviews, approvals | `reviewDecision`, `latestReviews { author, state }`                                          | ✓      | ✓        |
| review requested from me    | `reviewRequests`, matched against the identity set                                           |        | ✓        |
| failing CI                  | required checks in `statusCheckRollup` (see "Failing checks")                                | ✓      | ✓        |
| merge conflicts             | `mergeStateStatus == DIRTY`                                                                  | ✓      | ✓        |
| new activity                | see "Seen state"                                                                             | ✓      | ✓        |
| size                        | `additions`, `deletions`                                                                     |        | ✓        |
| age column                  | `updatedAt`                                                                                  | ✓      | ✓        |
| PR link                     | `url`                                                                                        | ✓      | ✓        |

`gh pr view --json` does not expose review-thread resolution, so unresolved
comments need the GraphQL query anyway. That is one more reason the batched
GraphQL query is the only fetch path, not a later optimisation. Top-level
conversation comments cannot be resolved. They count as activity, not as
unresolved comments.

Bot accounts such as CodeRabbit count as "other people". Their review threads
are exactly the feedback the user has to deal with.

Review requests are matched against the identity set directly. A request made
to a team the user belongs to does not count, because resolving team membership
costs another call per team.

GitHub reports only whether a PR conflicts, not which files conflict. A count
of conflicting files needs a local merge attempt (issue #100's
`conflict_count`), so the GitHub-side rollup does not carry one.

### Messages

```proto
enum Authorship {
  AUTHORSHIP_UNSPECIFIED = 0;
  AUTHORSHIP_MINE = 1;
  AUTHORSHIP_THEIRS = 2;
}

enum MergeStateStatus {
  MERGE_STATE_STATUS_UNSPECIFIED = 0;
  MERGE_STATE_STATUS_UNKNOWN = 1;
  MERGE_STATE_STATUS_CLEAN = 2;
  MERGE_STATE_STATUS_DIRTY = 3;
  MERGE_STATE_STATUS_BEHIND = 4;
  MERGE_STATE_STATUS_BLOCKED = 5;
  MERGE_STATE_STATUS_UNSTABLE = 6;
  MERGE_STATE_STATUS_HAS_HOOKS = 7;
  MERGE_STATE_STATUS_DRAFT = 8;
}

enum ReviewDecision {
  REVIEW_DECISION_UNSPECIFIED = 0;
  REVIEW_DECISION_REVIEW_REQUIRED = 1;
  REVIEW_DECISION_APPROVED = 2;
  REVIEW_DECISION_CHANGES_REQUESTED = 3;
}

enum ReviewState {
  REVIEW_STATE_UNSPECIFIED = 0;
  REVIEW_STATE_COMMENTED = 1;
  REVIEW_STATE_APPROVED = 2;
  REVIEW_STATE_CHANGES_REQUESTED = 3;
}

enum CheckState {
  CHECK_STATE_UNSPECIFIED = 0;
  CHECK_STATE_PENDING = 1;
  CHECK_STATE_PASSING = 2;
  CHECK_STATE_FAILING = 3;
}

message Review {
  string login = 1;
  ReviewState state = 2;
  bool is_me = 3;
}

message ChecksSummary {
  CheckState state = 1;
  int32 passing = 2;
  int32 failing = 3;
  int32 pending = 4;
}

message PullRequestState {
  string url = 1;
  string author_login = 2;
  bool is_draft = 3;
  google.protobuf.Timestamp updated_at = 4;
  MergeStateStatus merge_state_status = 5;
  ReviewDecision review_decision = 6;
  repeated Review latest_reviews = 7;
  bool review_requested_from_me = 8;
  int32 unresolved_thread_count = 9;
  ChecksSummary checks = 10;
  int32 additions = 11;
  int32 deletions = 12;
  bool has_new_activity = 13;
  int64 score = 14;
}

message Worktree {
  Authorship authorship = 13;
  optional PullRequestState pull_request = 14;
  bool authorship_overridden = 15;
}

service LumberjackService {
  rpc MarkViewed(MarkViewedRequest) returns (MarkViewedResponse);
  rpc SetAuthorship(SetAuthorshipRequest) returns (SetAuthorshipResponse);
}

message MarkViewedRequest {
  string repository = 1;
  Authorship authorship = 2;
  google.protobuf.Timestamp seen_through = 3;
}

message MarkViewedResponse {}

message SetAuthorshipRequest {
  string repository = 1;
  string worktree = 2;
  optional Authorship authorship = 3;
}

message SetAuthorshipResponse {
  Worktree worktree = 1;
}
```

Notes on the shape:

- The daemon always sends `AUTHORSHIP_MINE` or `AUTHORSHIP_THEIRS`.
  `AUTHORSHIP_UNSPECIFIED` only ever means the field was not sent — a daemon
  older than this change — and a client falls back to an unsplit list rather
  than showing two empty tabs.
- `MarkViewedRequest.authorship` accepts only `AUTHORSHIP_MINE` or
  `AUTHORSHIP_THEIRS`; `UNSPECIFIED` is `INVALID_ARGUMENT`.
- `SetAuthorshipRequest.authorship` absent is `auto`: it clears the override.
  Present, it must be `AUTHORSHIP_MINE` or `AUTHORSHIP_THEIRS`; `UNSPECIFIED`
  is `INVALID_ARGUMENT`.
- `SetAuthorshipRequest.worktree` is a branch or directory name, the same
  reference `DeleteWorktreeRequest.worktree` accepts; an unmatched one is
  `NOT_FOUND`. The response carries the updated worktree, so the caller can
  show where it moved.
- `authorship_overridden` is true when the value came from `set-authorship`
  rather than the conditions, so `lumberjack worktrees` and the app can mark it.
- `pull_request` is absent when the worktree has no PR, and also before the
  first fetch after daemon start. Absent means "not known yet". It never means
  "clean".
- `merge_state_status` mirrors GitHub's `mergeStateStatus` one-to-one; a merge
  conflict is `MERGE_STATE_STATUS_DIRTY`. GitHub computes it lazily, so a
  freshly pushed PR reports `UNKNOWN` for a while, which is never shown as a
  conflict. `UNSPECIFIED` means the field was not sent.
- `CHECK_STATE_UNSPECIFIED` means the PR has no required checks. That is
  different from `PENDING`.
- `latest_reviews` holds each reviewer's most recent review. It drives
  "Approved by dmitri" on `mine` and "You approved this" on `theirs` (`is_me`).
- Staleness is shown from the existing `Repository.last_synced_at` ("synced
  3 m ago" in the header). The rollup is fetched during sync, so the two always
  agree. Between refreshes (see "Refreshing the rollup"), data up to an hour old
  is normal.

### Seen state

"New" in the wireframe means new since the user last looked at that tab, for
that repository. GitHub cannot reconstruct that, so per `docs/schema.md` it is
stored:

```
list_views(repository_id, authorship, viewed_at)
```

`has_new_activity` is true when any of these happened after `viewed_at` for the
PR's tab:

| activity                    | timestamp compared                                                                   |
| --------------------------- | ------------------------------------------------------------------------------------ |
| the PR was newly synced     | `pull_requests.first_seen_at`                                                        |
| a new comment               | `createdAt` of the newest comment, review or thread reply not by the identity set    |
| a new merge conflict        | `pull_requests.conflicted_since` — when a sync first saw `mergeStateStatus == DIRTY` |
| a new failing check         | `pull_requests.checks_failing_since` — when a sync first saw a failing required check |

GitHub records when a comment was made, but not when a PR started conflicting
or when its required checks started failing, so those two are stored: a sync
sets the column the first time it observes the condition and clears it when the
condition goes away. A conflict that is fixed and then comes back is new again.

The same rule applies to both tabs. The app shows a dot and a darker subtitle
on each row with new activity, and a dot on a tab when any of its rows has it.

- The app calls `MarkViewed` when the user **leaves** a tab: by closing the
  panel, switching tab, or switching repository. If it marked on arrival, the
  markers would disappear while the user was still reading them.
- `viewed_at` is the time of the data the user saw, not the time they left.
  The app sends the `Repository.last_synced_at` it was displaying as
  `seen_through`, and the daemon stores that, never moving `viewed_at`
  backwards. Stamping the wall clock instead would mark as seen any activity
  that happened after the last sync, which the user never saw because it had
  not been fetched yet.
- A repository that has never been viewed has no row in `list_views`. Then
  nothing is new, which stops a first launch from lighting up every PR.

### Ordering

`ListWorktrees`, `GetRepository` and `Watch` return worktrees in display order:
`mine` by `score` descending, then `theirs` by `score` ascending. Equal scores
are broken by `updated_at`, most recent first. The ordering lives in the daemon
so that the CLI (Go) and the app (Swift) cannot drift apart.

`score` is the number the order came from, and the app uses it directly:

**`mine`: an attention score, highest first.** A score above zero counts
towards the badge on the Mine tab.

| signal                          | weight |
| ------------------------------- | ------ |
| new activity                    | +40    |
| changes requested               | +30    |
| merge conflict (`DIRTY`)        | +25    |
| failing required checks         | +25    |
| each unresolved thread, up to 3 | +8     |
| draft                           | −10    |

**`theirs`: an effective size, smallest first.** Rows with an effective size of
1,000 or more sit under a "Large or blocked" divider. The size chip's tooltip
shows the effective size.

| signal                                | weight |
| ------------------------------------- | ------ |
| lines changed (additions + deletions) | n      |
| failing required checks               | +800   |
| merge conflict (`DIRTY`)              | +600   |
| any unresolved threads                | +300   |

The weights are starting values to tune. Changing them needs no proto change.
`has_new_activity` feeds the `mine` score, so calling `MarkViewed` can reorder
the tab the next time it is opened.

### Delivery

- The rollup is fetched only at the three points in "Refreshing the rollup" —
  daemon start, the `Sync` RPC, and the hourly loop — and held in memory by the
  daemon. It is not written to the database. Because daemon start refreshes
  every repository, a restart leaves only a short window where it is missing.
- `ListWorktrees` and `GetRepository` serve the cached rollup. The app polls
  every 2 seconds, so these calls must never trigger a GitHub fetch.
  `has_new_activity` and `score` are recomputed per call from the cache,
  `pull_requests` and `list_views`, so `MarkViewed` takes effect on the next
  poll.
- `Watch`: `SYNC_FINISHED` also populates `worktrees` (field 3, today only set
  on `SNAPSHOT`), so a subscriber re-renders after each fetch without a
  follow-up call. No new event type is needed.

## Database changes

Per `docs/schema.md`, only state that git and `gh` cannot reconstruct is
stored. These new facts qualify: the user created a worktree locally; the user
overrode a worktree's authorship; a PR's author; that a PR has closed; when the
daemon first saw a PR, and when it first saw it conflicting or failing a
required check; and when the user last looked at each tab. The
authorship verdict itself is not stored. It is derived from those facts every
time the view is built.

### Why the verdict is not a column

`worktrees.authorship_override` holds what the user said, not the verdict: when
it is null the verdict is still derived. Storing the derived verdict directly
would be a cache of its inputs, and each input can change while the cached
verdict stays put:

- The user logs in to another `gh` account, so the identity set grows. Every
  `theirs` row authored by that account is now wrong.
- Somebody opens a PR on a branch the user created locally. Condition 2
  overrides condition 1, so the row should flip from `mine` to `theirs`.
- A worktree with no PR gets pushed. Condition 3 can now answer, but nothing
  rewrites the row.

Storing the inputs makes all three cases correct by construction. The one input
that would be lost without storage, that the worktree was created locally, is
`origin`.

### Soft-deleted pull requests

Today `ReplaceOpenPRs` deletes the `pull_requests` row of every PR that has left
the open set. That throws away the PR's author, which an orphaned worktree still
needs, and its `first_seen_at`, which a reopened PR should keep. So the row is
soft-deleted instead:

- A PR that leaves the open set has `deleted_at` set to the sync time. The row
  is never removed; only the repository's `ON DELETE CASCADE` removes rows.
- A PR that reappears in the open set — reopened — reuses its existing row: the
  upsert on `(repository_id, github_pr_number)` clears `deleted_at`. Its
  `first_seen_at` is untouched, so a reopened PR does not count as new.
- Every query that means "the open PRs" filters on `deleted_at IS NULL`. The
  authorship lookup for a worktree does not filter, so an orphaned worktree
  still finds its PR's author.

### Migration `00006`: authorship inputs (staging step 1)

```sql
-- +goose Up
ALTER TABLE worktrees ADD COLUMN origin TEXT NOT NULL DEFAULT 'adopted'
    CHECK (origin IN ('local', 'synced', 'adopted'));

ALTER TABLE worktrees ADD COLUMN authorship_override TEXT
    CHECK (authorship_override IN ('mine', 'theirs'));

ALTER TABLE pull_requests ADD COLUMN author_login TEXT NOT NULL DEFAULT '';

ALTER TABLE pull_requests ADD COLUMN deleted_at TIMESTAMP;

-- +goose Down
DELETE FROM pull_requests WHERE deleted_at IS NOT NULL;
ALTER TABLE pull_requests DROP COLUMN deleted_at;
ALTER TABLE pull_requests DROP COLUMN author_login;
ALTER TABLE worktrees DROP COLUMN authorship_override;
ALTER TABLE worktrees DROP COLUMN origin;
```

| change                       | values / notes                                                                                                                                                                                                              |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `worktrees.origin`           | `local` (`lumberjack worktree add`), `synced` (created by sync for an open PR), `adopted` (found on disk). Existing rows become `adopted`. Guessing `local` would mark branches `mine` that the user may never have created |
| `worktrees.authorship_override` | `mine` / `theirs`, set by `lumberjack set-authorship`; null means derive it, and `set-authorship auto` sets it back to null. Wins over every condition. Existing rows start null |
| `pull_requests.author_login` | Immutable for the life of the PR, so it is safe to persist, like `github_pr_number`. Existing rows start empty. **The backfill is the sync's upsert:** its `ON CONFLICT ... DO UPDATE` sets `author_login` alongside `branch_name` and `last_synced_at`, so the first sync after the migration — which runs at daemon start — fills every open PR. Until then an empty author resolves to `theirs` |
| `pull_requests.deleted_at`   | Set when the PR leaves the open set, cleared when it reopens; see "Soft-deleted pull requests". Null means open. PRs that closed before this migration were already hard-deleted and are not recovered |

### Migration `00007`: seen state (staging step 2)

```sql
-- +goose Up
ALTER TABLE pull_requests ADD COLUMN first_seen_at TIMESTAMP;

UPDATE pull_requests SET first_seen_at = COALESCE(last_synced_at, CURRENT_TIMESTAMP);

ALTER TABLE pull_requests ADD COLUMN conflicted_since TIMESTAMP;

ALTER TABLE pull_requests ADD COLUMN checks_failing_since TIMESTAMP;

CREATE TABLE list_views (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    authorship TEXT NOT NULL CHECK (authorship IN ('mine', 'theirs')),
    viewed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (repository_id, authorship)
);

-- +goose Down
DROP TABLE list_views;
ALTER TABLE pull_requests DROP COLUMN checks_failing_since;
ALTER TABLE pull_requests DROP COLUMN conflicted_since;
ALTER TABLE pull_requests DROP COLUMN first_seen_at;
```

| change                        | values / notes                                                                                                                                                          |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `pull_requests.first_seen_at` | Set on insert, never updated — including when a soft-deleted PR reopens and its row is reused. Feeds `has_new_activity`. SQLite will not add a column with a `CURRENT_TIMESTAMP` default, so it is nullable and backfilled. Inserts always set it |
| `pull_requests.conflicted_since` | Set by the first sync that sees `mergeStateStatus == DIRTY`, left alone while it stays `DIRTY`, cleared by the first sync that sees anything else except `UNKNOWN` (which is GitHub still computing, not a resolution). Existing rows start null, so conflicts present at migration time count as new once |
| `pull_requests.checks_failing_since` | Set by the first sync that sees a failing required check (see "Failing checks"), left alone while any stays failing, cleared once none are. Existing rows start null |
| `list_views`                  | One row per repository and tab, upserted by `MarkViewed`. The `CHECK` limits `authorship` to `mine` / `theirs`. There is no row until the tab has been viewed once, so nothing is new on first launch |

`first_seen_at` is when the daemon first saw the PR, not GitHub's `createdAt`.
A PR that has been open for a week, in a repository the user only just added,
should not count as new.

### Not stored

| state                                           | where it lives                                                                                                                           |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| authorship verdict                              | derived per request from `authorship_override`, `origin`, `author_login` and the identity set                                            |
| condition 3's commit authors                    | in memory, keyed on head SHA, alongside the existing `noPRBranches` cache. A restart costs one `compare` call per unlinked pushed branch |
| the PR rollup (reviews, checks, size, activity) | in memory, refreshed each sync                                                                                                           |
| `has_new_activity`, `score`                     | derived per request from the rollup, `pull_requests` and `list_views`                                                                    |

## Proposed staging

Each of these is a mergeable PR on its own.

1. **Authorship modelling.** Migration `00006`, soft-deleted `pull_requests`,
   `AuthorLogin` on `github.PR`, sync wiring, the identity set, the resolver,
   the `Authorship` enum on the worktree view, `SetAuthorship` and
   `lumberjack set-authorship`, and surfacing in `lumberjack worktrees` and the
   macOS app.

   Worth splitting: the override, conditions 1 and 2, and the identity set are
   one PR and deliver most of the value; condition 3, which needs the `compare`
   fetch and its SHA cache, is a second. Until condition 3 lands, a PR-less
   adopted worktree is `theirs`, which `set-authorship` corrects.

2. **Mergeability rollup.** The batched GraphQL fetch, `PullRequestState` on
   the worktree view, migration `00007` and `MarkViewed`, and server-side
   ordering. Coordinate with issue #100 so there is one merge-state story, not
   two.

## Open questions

- **Interaction with issue #84 (sync scope).** Authorship is arguably a scope
  dimension too: "sync my PRs only" is a thing people will want. Whether that is
  a fourth scope mode or an orthogonal filter should be settled before #84 lands.
- **Condition 3's cost.** One `compare` call per unlinked, pushed, PR-less
  worktree. Cheap individually, not free in aggregate. Results cache on the
  immutable head SHA, so steady-state cost is zero and only a moved head pays.
