# PR Ownership, Mergeability, and Prompt-Driven Automation

Status: design. Nothing here is implemented yet.

This spec introduces a first-class distinction between pull requests **the user
authored** and pull requests **other people authored that we sync**, and builds
two automation lifecycles on top of it. It extends `docs/schema.md`; where the
two disagree, this document is the newer intent.

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

The macOS app splits its PR list into `mine` and `theirs`, and orders each list
by how much it needs the user. Everything it needs comes from the worktree view,
so the change is additive fields on `Worktree` and nothing else. Each new field
is new, so `buf breaking` passes and an already-released app ignores them.

### What the views need

| need                            | source                                                                   | `mine` | `theirs` |
| ------------------------------- | ------------------------------------------------------------------------ | ------ | -------- |
| which list a PR belongs in      | `authorship` column                                                      | ✓      | ✓        |
| unresolved comments             | GraphQL `reviewThreads { isResolved }`                                   | ✓      | ✓        |
| blocking reviews                | `reviewDecision`, plus `latestReviews` in state `CHANGES_REQUESTED`      | ✓      | ✓        |
| failing CI                      | `statusCheckRollup`                                                      | ✓      | ✓        |
| merge conflicts                 | `mergeable`                                                              | ✓      | ✓        |
| new activity from other people  | newest comment, review or thread reply not authored by the identity set  | ✓      | ✓        |
| size                            | `additions`, `deletions`, `changedFiles`                                 |        | ✓        |
| row label and link              | `title`, `url`                                                           | ✓      | ✓        |

`gh pr view --json` does not expose review-thread resolution, so unresolved
comments need the GraphQL query anyway. That is one more reason the batched
GraphQL query is the only fetch path, not a later optimisation. Top-level
conversation comments cannot be resolved. They count as activity, not as
unresolved comments.

Bot accounts such as CodeRabbit count as "other people". Their review threads
are exactly the feedback the user has to deal with.

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

enum CheckState {
  CHECK_STATE_UNSPECIFIED = 0;
  CHECK_STATE_PENDING = 1;
  CHECK_STATE_PASSING = 2;
  CHECK_STATE_FAILING = 3;
}

message ChecksSummary {
  CheckState state = 1;
  int32 passing = 2;
  int32 failing = 3;
  int32 pending = 4;
  repeated string failing_names = 5;
}

message PullRequestSize {
  int32 additions = 1;
  int32 deletions = 2;
  int32 changed_files = 3;
}

message PullRequestState {
  google.protobuf.Timestamp fetched_at = 1;
  string title = 2;
  string url = 3;
  string author_login = 4;
  bool is_draft = 5;
  string head_oid = 6;
  Mergeable mergeable = 7;
  ReviewDecision review_decision = 8;
  repeated string changes_requested_by = 9;
  int32 unresolved_thread_count = 10;
  ChecksSummary checks = 11;
  PullRequestSize size = 12;
  optional google.protobuf.Timestamp last_activity_by_others_at = 13;
  int64 priority = 14;
}

message Worktree {
  Authorship authorship = 13;
  optional PullRequestState pull_request = 14;
}
```

Notes on the shape:

- `AUTHORSHIP_UNSPECIFIED` doubles as `unknown`. Clients render it in neither
  list, or in a third "unclassified" group, and never as `mine`. That is the same
  rule automation follows.
- `pull_request` is absent when the worktree has no PR, and also before the
  first fetch after daemon start. Absent means "not known yet". It never means
  "clean".
- `MERGEABLE_UNSPECIFIED` also covers GitHub's `UNKNOWN`. GitHub computes
  mergeability lazily, so a freshly pushed PR reports `UNKNOWN` for a while.
- `CHECK_STATE_UNSPECIFIED` means the PR has no checks. That is different from
  `PENDING`.
- `fetched_at` is how the UI shows staleness. With an hourly fetch, data up to
  an hour old is normal, and the user should be able to see that.

### Ordering

`priority` is an opaque sort key computed by the daemon: higher sorts first. It
only compares within one authorship. A combined view groups by `authorship`
first, then sorts by `priority` within each group.

The daemon computes it, rather than each client sorting, for three reasons:

- `ListWorktrees` is per repository, and the app merges lists across
  repositories. A scalar key lets it do that without knowing the rules.
- The CLI (Go) and the app (Swift) would otherwise implement the same ordering
  twice and drift apart.
- The weighting will need tuning. Because the key is opaque, tuning it needs no
  proto change.

The rules the key encodes:

**`mine`, most actionable first.** A PR needs the user if it has any of the
following: changes requested, unresolved threads, a merge conflict, or failing
checks. PRs that need the user sort above those that don't. Within each group,
the newest `last_activity_by_others_at` sorts first. Drafts sort with everything
else, since they are the user's own work in progress.

**`theirs`, highest value first.** The base is lines changed
(`additions + deletions`), with the smallest first. Each of the following
doubles the effective size: failing checks, a merge conflict, and unresolved
threads. A PR under 1000 effective lines always sorts above one over 1000.
Drafts sort last.

Once ranking (lifecycle B) lands, its score replaces the size heuristic for
`theirs` inside the daemon. Clients see the same `priority` field and need no
change.

### Delivery

- The rollup is fetched during sync, hourly or on `lumberjack sync`, and held
  in memory by the daemon. It is not written to the database. Sync runs straight
  away on daemon start, so a restart leaves only a short window where it is
  missing.
- `ListWorktrees` and `GetRepository` serve the cached rollup. The app polls
  every 2 seconds, so these calls must never trigger a GitHub fetch.
- `Watch`: `SYNC_FINISHED` also populates `worktrees` (field 3, today only set
  on `SNAPSHOT`), so a subscriber re-renders after each fetch without a
  follow-up call. No new event type is needed.

## Lifecycle A: owned PRs

The path is worktree → commits → PR → _feedback loop_ → merge. The first three
steps already work. The feedback loop is what this adds.

The trigger is a change to the PR's head: a new push, a new review, or a check
run completing. Polling `gh` for that on a tight loop is the wrong shape — the
existing hourly sync tick is far too slow to feel responsive, and a one-minute
poll per PR is exactly the cost issue #101 is already about. The right
mechanism is a separate, cheap watch: one batched GraphQL query per repository
returning head OID, review decision and check rollup for every owned open PR,
on its own short interval, with the expensive per-PR work done only for PRs
whose head OID or rollup actually moved.

When one moves, and only for **owned** PRs, Lumberjack runs the configured
prompts against that PR's worktree. Each prompt is a named unit with a
condition it fires on:

```yaml
automation:
  provider: claude
  owned:
    - name: fix-review-feedback
      on: review_changes_requested
      prompt: |
        Address every unresolved review comment on this pull request.
        Make the smallest change that resolves each one.
    - name: fix-checks
      on: checks_failed
      prompt: |
        The static analysis checks on this pull request are failing.
        Read the failing check output and fix the cause.
```

Non-negotiable constraints on this loop:

- **It only ever runs on `owned`.** `incoming` and `unknown` are never touched.
- **It runs on a clean worktree only.** An agent editing a worktree with
  uncommitted user changes can destroy work that exists nowhere else.
- **It never pushes on its own.** It commits to the branch and stops. Pushing is
  a separate, explicitly opted-in step; an automation loop that pushes can
  re-trigger itself.
- **It is bounded.** A maximum number of consecutive automated attempts per head
  OID, after which the worktree is marked as needing the user and left alone.
  Without this, a prompt that cannot fix the problem burns tokens forever.
- **Its config is trusted config.** See "Trust", below.

## Lifecycle B: incoming PRs

Incoming PRs need reviewing, and the question the user actually has is _which
one first_. So each incoming PR gets a rank.

Ranking runs when an incoming PR's head changes — the same watch that drives
lifecycle A — and produces a score plus a one-line rationale. Inputs are the
PR's title, body, author, size (files and lines changed), age, whether the user
is a requested reviewer, and its mergeability rollup.

The rank is a cache of a model's judgement about a specific head OID, not a
fact about the PR, and it is expensive to recompute. That makes it the one
piece of derived state here worth persisting — keyed on the head OID it was
computed for, so it self-invalidates on the next push:

```
pr_rankings(repository_id, github_pr_number, head_oid, score, rationale, provider, ranked_at)
```

A ranking whose `head_oid` no longer matches the live head is stale and is
shown as such, or ignored, rather than being trusted.

There is a **default ranking prompt compiled into the binary**, so this works
with no configuration. `.lumberjack.yml` may override it:

```yaml
automation:
  incoming:
    ranking:
      prompt: |
        Rank this pull request by how urgently it needs my review.
```

The prompt must be readable from the CLI (`lumberjack ranking-prompt`, or
similar) — the user needs to see what is actually being asked before trusting a
number, and needs to see the default in order to base an override on it.

## The provider abstraction

Ranking and fix-feedback must not be bound to one vendor. Introduce a provider
interface and one implementation.

```go
// internal/llm
type Provider interface {
    Name() string
    Complete(ctx context.Context, req Request) (Response, error)
}
```

`Request` carries the prompt, the input facts, and an optional JSON schema the
response must satisfy. `Response` carries the text or the decoded structured
result. This is deliberately the _narrow_ surface — a single structured
completion — because that is all ranking needs, and a narrow interface is one a
second provider can actually implement.

The fix-feedback loop needs more than that: an agent that can read and write
files in the worktree and run commands. That is a different capability and
should be a **separate, optional interface** (`Agent`, say) that a provider may
or may not implement, rather than being forced into `Provider`. Ranking works
with any provider; fix-feedback only works with providers that are agentic.

The first and only implementation is Claude. Selection is by name in
`.lumberjack.yml` (`provider: claude`), defaulting to Claude when unset, so
adding a second provider is a registry entry rather than a refactor.

Credentials stay out of the database, exactly as `gh` tokens do (`docs/schema.md`,
"Not stored"). The Claude provider reads its key from the environment.

## Trust

This is the part most likely to go wrong, so it is stated explicitly.

`.lumberjack.yml` already has a trust model, and it exists because a `run_command`
step is arbitrary code execution: config is read from the **trusted default
branch**, never from the branch being cloned, and `run_command` steps require
recorded consent keyed on the file's checksum (`trusted_setup_steps`).

**A prompt that drives an agent with write access to a worktree is at least as
dangerous as a `run_command`, and must inherit exactly the same treatment.** The
`automation` block is trusted config:

- Read from the default branch tip, never from a PR branch.
- Covered by the same checksum consent — adding an `automation` block changes
  the file's checksum, which invalidates existing consent and re-prompts. That
  falls out of the existing mechanism for free.

The sharper version of the same problem is ranking. Ranking an incoming PR
means feeding an untrusted author's title and body to a model. If that model
can also be steered by a prompt from the branch, a PR can rank itself first, or
worse. Hence: the ranking prompt comes from the trusted default branch only,
the PR's own content is passed as **data to be ranked and never as
instructions**, and the ranking provider is given no tools and no write access —
it returns a score and a string, nothing else.

## Proposed staging

Each of these is a mergeable PR on its own.

1. **Ownership modelling.** `origin` on `worktrees`, `author_login` on
   `pull_requests`, migration `00006`, `AuthorLogin` on `github.PR`, sync
   wiring, the identity set, the three-condition resolver, the `Ownership` enum
   on the worktree view, and surfacing in `lumberjack worktrees` and the macOS
   app. No automation. This is the whole safety boundary and everything else
   depends on it.

   Worth splitting: conditions 1 and 2 plus the identity set are one PR and
   deliver most of the value; condition 3, which needs the `compare` fetch and
   its SHA cache, is a second.

2. **Mergeability rollup.** The batched GraphQL fetch and `PullRequestState` on
   the worktree view. Coordinate with issue #100 so there is one merge-state
   story, not two.
3. **The head-change watch.** The cheap per-repository poll that detects a moved
   head OID or a changed rollup, with the per-PR work gated on it. Also the fix
   for the cost half of issue #101.
4. **The provider interface and the Claude implementation.** `internal/llm`,
   the narrow `Provider`, the default ranking prompt, and `automation` parsing
   in `internal/setup`. No behaviour wired to it yet.
5. **Ranking for incoming PRs.** `pr_rankings`, the ranking run on head change,
   rank display and sort in `lumberjack worktrees`, and the command that prints
   the effective prompt.
6. **Fix-feedback for owned PRs.** The `Agent` interface, the prompt runner, and
   every constraint listed under lifecycle A. Last, deliberately — it is the
   only part that writes to the user's code.

## Open questions

- **"New" activity.** `last_activity_by_others_at` orders `mine` by how recent
  the activity is. It cannot say whether the user has already seen that
  activity. Highlighting unseen activity needs a persisted "seen at" marker per
  PR, plus an RPC to set it. That marker is user state GitHub cannot
  reconstruct, so it would be stored. Add it only if recency alone proves not
  to be enough.

- **Interaction with issue #84 (sync scope).** Ownership is arguably a scope
  dimension too: "sync my PRs only" is a thing people will want. Whether that is
  a fourth scope mode or an orthogonal filter should be settled before #84 lands.
- **Condition 3's cost.** One `compare` call per unlinked, pushed, PR-less
  worktree. Cheap individually, not free in aggregate. Results cache on the
  immutable head SHA, so steady-state cost is zero and only a moved head pays.
- **Fork PRs.** An incoming PR from a fork has a head branch that does not exist
  on the origin remote. Worktree creation for those is a pre-existing question,
  but ranking makes it more pressing, since fork PRs are exactly the incoming
  ones.
- **What "handled all the feedback" means.** Lifecycle A's stated goal is
  reaching a mergeable state, but nothing here defines the terminal condition
  precisely enough to stop on. `mergeStateStatus == CLEAN` plus
  `reviewDecision == APPROVED` is the obvious candidate and is probably right.
- **Cost controls.** There is no budget concept anywhere in this spec. Before
  lifecycle A ships, there needs to be a ceiling the user sets and can see.
