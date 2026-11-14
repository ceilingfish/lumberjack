# PR Ownership and Mergeability

Status: design. Nothing here is implemented yet.

This spec introduces a first-class distinction between pull requests **the user
authored** and pull requests **other people authored that we sync**, and shows
how far along each one is. It extends `docs/schema.md`; where the two disagree,
this document is the newer intent.

Automating the user's own PRs, which builds on this classification, is tracked
separately in issue #110.

## Why ownership is the foundation

Today every tracked worktree is treated identically: sync creates one per open
PR, reconciles it, and removes it when the PR leaves the open set. That is
correct for the mechanics of worktree management and wrong for everything we
want to do next, because the two kinds of change have opposite goals:

- An **owned** change is work in progress. The goal is to drive it to the point
  where it can merge — all review feedback addressed, all static analysis green.
  The user wants Lumberjack to _push it forward_.
- An **incoming** change is someone else's work awaiting the user's attention.
  The goal is to tell the user which one to look at first, and to notice when it
  changes under them. The user wants Lumberjack to _triage it_.

## Determining authorship

We are going to extend the worktree table with an `authorship` column. This can either be `mine` or `theirs`. `mine` indicates it was created by the owning user, `thiers` indicates it was synchronised in

### 1. The user created it locally

`lumberjack worktree add` is an unambiguous statement of intent: the user asked for this branch, on this machine. When the user does this we should automatically set authorship to `mine`.

### 2. The PR is authored by the logged-in account

If the PR was created by the same user that created the user that is currently associated with the repo gh login. i.e. the login from the associated repository.

If we synchronise in a repository then we should take the PR owner as the owner, and set the ownership to `mine` if it matches the repository's gh user, or `theirs` if it is a different user.

Once a PR exists, its author is the authoritative statement of whose change it
is, and it is immutable for the life of the PR — the same property that makes
`github_pr_number` safe to persist on `worktrees`. So `pull_requests` gains
`author_login`, populated from the sync fetch.

`github.PR` gains `AuthorLogin`, and `ListOpenPRs` adds `author` to its
`--json` selection (`gh` returns `author.login`). `FindPRForBranch` adds the
same, so a PR discovered after the fact between sync ticks is classified too.

Ownership is then "the author is one of the user's identities" — see "Identity
sets" below, which is not the single-login comparison an earlier draft of this
spec assumed. The comparison is **derived at read time, not stored**: both
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
  wrote it, which is the question ownership asks.
- **`author` may be null.** An address belonging to no GitHub account resolves
  to nothing. Null means _unknown_, and must fall to the `unknown` ownership
  state — never to `incoming`.
- **`compare/BASE...HEAD` is the right endpoint.** It returns exactly the
  commits between the merge base and the head in a single call, already
  resolved. `commits?sha=<branch>` walks the entire history and forces the
  caller to paginate until it finds the divergence point.

So condition 3 resolves in layers, and needs no local identity matching at all:

| branch state  | source                                                                                                       |
| ------------- | ------------------------------------------------------------------------------------------------------------ |
| PR exists     | `gh pr view --json commits` → `authors[].login`, already fetched; no extra call                              |
| pushed, no PR | `gh api repos/O/N/compare/<default>...<branch>`                                                              |
| never pushed  | GitHub knows nothing — and a branch that exists only locally is the user's by construction. Owned, no lookup |

An empty commit range (branch tip equals the merge base) is **not** owned — a
freshly adopted branch with no commits of its own tells us nothing, and
defaulting to owned arms automation on the strength of no evidence.

Author resolution is keyed on an immutable commit SHA, so it is cacheable
indefinitely rather than per-tick.

Two limits to respect: `compare` truncates at 250 commits, so a very long branch
needs the paginated commits endpoint or an explicit "too large to classify"
answer; and the API only knows commits that have been pushed.

### Identity sets

A single `repositories.login` comparison is **not sufficient**, and this
repository is itself the counterexample. It is registered under login
`ceilingfish`, its local `user.email` is `tom.parrish@parall.ax` resolving to
`ceilingfish-parallax`, and both accounts author commits on it. Under a
single-login check the user's own PRs classify as `incoming`.

One human routinely has several GitHub accounts — personal and work, or one per
client — and `gh` already models this: it authenticates several accounts per
host, and `ListLogins` already returns them. So ownership compares the PR author
against an **identity set**:

1. `repositories.login`, the account the repo was registered under.
2. Every other login `gh` is authenticated as for that host (`gh auth status`),
   which is already available via `github.Client.ListLogins`.
3. Any additional logins configured per-repository, for accounts `gh` does not
   hold — a shared bot, or a second machine's account.

Comparison is case-insensitive; GitHub logins are not case-sensitive. Set 2
means the common multi-account case needs no configuration at all, which is why
it is worth preferring over asking the user to list identities up front.

This needs CLI surface to inspect and amend — the user must be able to see why
a PR was classified the way it was, and correct it.

### Resolution order

```
if the worktree has a PR:          condition 2 decides, alone
else if origin == 'local':         owned
else:                              condition 3 decides
```

Condition 2 is deliberately exclusive rather than an `OR` with the others. If
the user created a worktree locally and somebody else opened the PR on that
branch, the PR author is right and the local provenance is stale. An `OR` would
let condition 1 permanently out-vote reality.

The result is a three-valued `Ownership`: `owned`, `incoming`, `unknown`.
`unknown` is a real state — no PR, not locally created, and no resolvable git
identity — and it is the state in which **no automation runs**. Every
automation path must treat `unknown` as `incoming`, never as `owned`.

## Mergeability state

Ownership says which lifecycle a PR is in; mergeability says how far along it
is. All of it is mutable and all of it is reconstructible from `gh`, so per
`docs/schema.md` **none of it is stored** — it is fetched live and folded into
the worktree view alongside the existing reconciliation status.

From `gh pr view --json mergeable,mergeStateStatus,reviewDecision,statusCheckRollup,isDraft`:

| field               | use                                                             |
| ------------------- | --------------------------------------------------------------- |
| `mergeable`         | `MERGEABLE` / `CONFLICTING` / `UNKNOWN` — conflicts with base   |
| `mergeStateStatus`  | `CLEAN`, `BLOCKED`, `BEHIND`, `DIRTY`, ... — why it can't merge |
| `reviewDecision`    | `APPROVED` / `CHANGES_REQUESTED` / `REVIEW_REQUIRED`            |
| `statusCheckRollup` | per-check conclusions — the static analysis signal              |
| `isDraft`           | drafts are never automation candidates                          |

This is the same surface issue #100 proposes as `MergeState`, and it should be
one message rather than two overlapping ones. #100's `base_commit_sha` and
`conflict_count` describe a _local_ merge attempt; the fields above describe
GitHub's view. Both belong on the worktree view, and the naming should make
clear which is which — suggest `MergeState` for the local attempt (as #100 has
it) and `PullRequestState` for the GitHub-side rollup.

`GetRepository`, `ListWorktrees` and `Watch` all carry the worktree view, so
all three gain it at once. `Authorship` is an enum on the same message. See
"gRPC surface" below for the wire shape.

Cost is the open problem: this is one `gh pr view` per PR per tick, and issue
#101 already reports sync scaling badly with worktree count. The rollup should come from a single batched GraphQL query over the repository's open PRs
(`gh api graphql`), not N per-PR calls. It should, however, be fetched hourly or whenever `lumberjack sync` is invoked.

## gRPC surface

The macOS app shows one repository at a time, chosen from a dropdown. It splits
that repository's PRs into two tabs, `mine` and `theirs`, the latter labelled
"Incoming", and orders each tab by how much it needs the user. Worktrees the
daemon cannot classify appear in neither tab. The wire changes are additive fields on `Worktree` and one new RPC.
Because every field and RPC is new, `buf breaking` passes and an
already-released app ignores them.

### What the views need

| need                         | source                                                                  | `mine` | `theirs` |
| ---------------------------- | ----------------------------------------------------------------------- | ------ | -------- |
| which tab a PR belongs in    | `authorship` column                                                     | ✓      | ✓        |
| unresolved comments          | GraphQL `reviewThreads { isResolved }`                                  | ✓      | ✓        |
| blocking reviews, approvals  | `reviewDecision`, `latestReviews { author, state }`                     | ✓      | ✓        |
| review requested from me     | `reviewRequests`, matched against the identity set                      |        | ✓        |
| failing CI                   | `statusCheckRollup`                                                     | ✓      | ✓        |
| merge conflicts              | `mergeable`                                                             | ✓      | ✓        |
| new activity from others     | newest comment, review or thread reply not by the identity set, against the last-viewed time | ✓ |  |
| new PRs since last looked    | when the daemon first saw the PR, against the last-viewed time           |        | ✓        |
| size                         | `additions`, `deletions`                                                |        | ✓        |
| age column                   | `updatedAt`                                                             | ✓      | ✓        |
| PR link                      | `url`                                                                   | ✓      | ✓        |

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

enum Mergeable {
  MERGEABLE_UNSPECIFIED = 0;
  MERGEABLE_MERGEABLE = 1;
  MERGEABLE_CONFLICTING = 2;
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
  Mergeable mergeable = 5;
  ReviewDecision review_decision = 6;
  repeated Review latest_reviews = 7;
  bool review_requested_from_me = 8;
  int32 unresolved_thread_count = 9;
  ChecksSummary checks = 10;
  int32 additions = 11;
  int32 deletions = 12;
  bool has_new_activity = 13;
  bool is_new = 14;
  int64 score = 15;
}

message Worktree {
  Authorship authorship = 13;
  optional PullRequestState pull_request = 14;
}

service LumberjackService {
  rpc MarkViewed(MarkViewedRequest) returns (MarkViewedResponse);
}

message MarkViewedRequest {
  string repository = 1;
  Authorship authorship = 2;
}

message MarkViewedResponse {}
```

Notes on the shape:

- `AUTHORSHIP_UNSPECIFIED` doubles as `unknown`. Clients leave it out of both
  tabs and never show it as `mine`. That is the same rule automation follows.
- `pull_request` is absent when the worktree has no PR, and also before the
  first fetch after daemon start. Absent means "not known yet". It never means
  "clean".
- `MERGEABLE_UNSPECIFIED` also covers GitHub's `UNKNOWN`. GitHub computes
  mergeability lazily, so a freshly pushed PR reports `UNKNOWN` for a while.
- `CHECK_STATE_UNSPECIFIED` means the PR has no checks. That is different from
  `PENDING`.
- `latest_reviews` holds each reviewer's most recent review. It drives
  "Approved by dmitri" on `mine` and "You approved this" on `theirs` (`is_me`).
- Staleness is shown from the existing `Repository.last_synced_at` ("synced
  3 m ago" in the header). The rollup is fetched during sync, so the two always
  agree. With an hourly fetch, data up to an hour old is normal.

### Seen state

"New" in the wireframe means new since the user last looked at that tab, for
that repository. GitHub cannot reconstruct that, so per `docs/schema.md` it is
stored:

```
list_views(repository_id, authorship, viewed_at)
```

- `has_new_activity` is true when the newest activity by someone outside the
  identity set is later than `viewed_at` for the PR's tab. The wireframe shows
  it as a dot and a darker subtitle on `mine` rows.
- `is_new` is true when the daemon first saw the PR after `viewed_at` for its
  tab. The app shows the unread dot on the Incoming tab when any `theirs` row has
  it.
- The app calls `MarkViewed` when the user **leaves** a tab: by closing the
  panel, switching tab, or switching repository. If it marked on arrival, the
  markers would disappear while the user was still reading them.
- A repository that has never been viewed has no row in `list_views`. Then
  nothing is new, which stops a first launch from lighting up every PR.

### Ordering

`ListWorktrees`, `GetRepository` and `Watch` return worktrees in display order:
`mine` by `score` descending, then `theirs` by `score` ascending, then
unclassified. The ordering lives in the daemon so that the CLI (Go) and the app
(Swift) cannot drift apart.

`score` is the number the order came from, and the app uses it directly:

**`mine`: an attention score, highest first.** A score above zero counts
towards the badge on the Mine tab.

| signal                          | weight |
| ------------------------------- | ------ |
| new activity from others        | +40    |
| changes requested               | +30    |
| merge conflict                  | +25    |
| failing checks                  | +25    |
| each unresolved thread, up to 3 | +8     |
| draft                           | −10    |

**`theirs`: an effective size, smallest first.** Rows with an effective size of
1,000 or more sit under a "Large or blocked" divider. The size chip's tooltip
shows the effective size.

| signal                                | weight |
| ------------------------------------- | ------ |
| lines changed (additions + deletions) | n      |
| failing checks                        | +800   |
| merge conflict                        | +600   |
| any unresolved threads                | +300   |

The weights are starting values to tune. Changing them needs no proto change.
`has_new_activity` feeds the `mine` score, so calling `MarkViewed` can reorder
the tab the next time it is opened.

### Delivery

- The rollup is fetched during sync, hourly or on `lumberjack sync`, and held
  in memory by the daemon. It is not written to the database. Sync runs straight
  away on daemon start, so a restart leaves only a short window where it is
  missing.
- `ListWorktrees` and `GetRepository` serve the cached rollup. The app polls
  every 2 seconds, so these calls must never trigger a GitHub fetch.
  `has_new_activity`, `is_new` and `score` are recomputed per call from the
  cache and `list_views`, so `MarkViewed` takes effect on the next poll.
- `Watch`: `SYNC_FINISHED` also populates `worktrees` (field 3, today only set
  on `SNAPSHOT`), so a subscriber re-renders after each fetch without a
  follow-up call. No new event type is needed.

## Database changes

Per `docs/schema.md`, only state that git and `gh` cannot reconstruct is
stored. Four new facts qualify: the user created a worktree locally; a PR's
author; extra logins the user has claimed; and when the user last looked at
each tab. The authorship verdict itself is not stored. It is derived from those
facts every time the view is built.

### Why the verdict is not a column

Storing `worktrees.authorship` directly would be a cache of three inputs, and
each input can change while the cached verdict stays put:

- The user logs in to another `gh` account, so the identity set grows. Every
  `theirs` row authored by that account is now wrong.
- Somebody opens a PR on a branch the user created locally. Condition 2
  overrides condition 1, so the row should flip from `mine` to `theirs`.
- A worktree with no PR gets pushed. Condition 3 can now answer, but nothing
  rewrites the row.

Storing the inputs makes all three cases correct by construction. The one input
that would be lost without storage, that the worktree was created locally, is
`origin`.

### Migration `00006`: authorship inputs (staging step 1)

```sql
-- +goose Up
ALTER TABLE worktrees ADD COLUMN origin TEXT NOT NULL DEFAULT 'adopted';

ALTER TABLE pull_requests ADD COLUMN author_login TEXT NOT NULL DEFAULT '';

CREATE TABLE repository_identities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    login TEXT NOT NULL COLLATE NOCASE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (repository_id, login)
);

CREATE INDEX idx_repository_identities_repository_id ON repository_identities (
    repository_id
);

-- +goose Down
DROP TABLE repository_identities;
ALTER TABLE pull_requests DROP COLUMN author_login;
ALTER TABLE worktrees DROP COLUMN origin;
```

| change                         | values / notes                                                                                                                         |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------- |
| `worktrees.origin`             | `local` (`lumberjack worktree add`), `synced` (created by sync for an open PR), `adopted` (found on disk). Existing rows become `adopted`. Guessing `local` would mark branches `mine` that the user may never have created |
| `pull_requests.author_login`   | Immutable for the life of the PR, so it is safe to persist, like `github_pr_number`. Existing rows start empty and are filled by the first sync, which runs at daemon start. Empty means unclassified |
| `repository_identities`        | Identity set 3: logins the user has claimed that `gh` does not hold. Sets 1 and 2 are `repositories.login` and `gh auth status`, which are already available. `COLLATE NOCASE` because GitHub logins are case-insensitive |

### Migration `00007`: seen state (staging step 2)

```sql
-- +goose Up
ALTER TABLE pull_requests ADD COLUMN first_seen_at TIMESTAMP;

UPDATE pull_requests SET first_seen_at = COALESCE(last_synced_at, CURRENT_TIMESTAMP);

CREATE TABLE list_views (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    authorship TEXT NOT NULL,
    viewed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (repository_id, authorship)
);

-- +goose Down
DROP TABLE list_views;
ALTER TABLE pull_requests DROP COLUMN first_seen_at;
```

| change                        | values / notes                                                                                                                         |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| `pull_requests.first_seen_at` | Set on insert, never updated. Drives `is_new`. SQLite will not add a column with a `CURRENT_TIMESTAMP` default, so it is nullable and backfilled. Inserts always set it |
| `list_views`                  | One row per repository and tab (`mine` / `theirs`), upserted by `MarkViewed`. There is no row until the tab has been viewed once, so nothing is new on first launch |

`first_seen_at` is when the daemon first saw the PR, not GitHub's `createdAt`.
A PR that has been open for a week, in a repository the user only just added,
should not count as new.

### Not stored

| state                                          | where it lives                                                                 |
| ---------------------------------------------- | ------------------------------------------------------------------------------ |
| authorship verdict                             | derived per request from `origin`, `author_login` and the identity set        |
| condition 3's commit authors                   | in memory, keyed on head SHA, alongside the existing `noPRBranches` cache. A restart costs one `compare` call per unlinked pushed branch |
| the PR rollup (reviews, checks, size, activity) | in memory, refreshed each sync                                                |
| `has_new_activity`, `is_new`, `score`          | derived per request from the rollup and `list_views`                           |

## Proposed staging

Each of these is a mergeable PR on its own.

1. **Ownership modelling.** `origin` on `worktrees`, `author_login` on
   `pull_requests`, migration `00006`, `AuthorLogin` on `github.PR`, sync
   wiring, the identity set, the three-condition resolver, the `Ownership` enum
   on the worktree view, and surfacing in `lumberjack worktrees` and the macOS
   app. This is the safety boundary that issue #110's automation depends on.

   Worth splitting: conditions 1 and 2 plus the identity set are one PR and
   deliver most of the value; condition 3, which needs the `compare` fetch and
   its SHA cache, is a second.

2. **Mergeability rollup.** The batched GraphQL fetch, `PullRequestState` on
   the worktree view, migration `00007` and `MarkViewed`, and server-side
   ordering. Coordinate with issue #100 so there is one merge-state story, not
   two.

## Open questions

- **Interaction with issue #84 (sync scope).** Ownership is arguably a scope
  dimension too: "sync my PRs only" is a thing people will want. Whether that is
  a fourth scope mode or an orthogonal filter should be settled before #84 lands.
- **Condition 3's cost.** One `compare` call per unlinked, pushed, PR-less
  worktree. Cheap individually, not free in aggregate. Results cache on the
  immutable head SHA, so steady-state cost is zero and only a moved head pays.
- **Fork PRs.** An incoming PR from a fork has a head branch that does not exist
  on the origin remote. Worktree creation for those is a pre-existing question,
  but the Incoming tab makes it more pressing, since fork PRs are exactly the
  incoming ones.
