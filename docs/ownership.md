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
  The user wants Lumberjack to *push it forward*.
- An **incoming** change is someone else's work awaiting the user's attention.
  The goal is to tell the user which one to look at first, and to notice when it
  changes under them. The user wants Lumberjack to *triage it*.

Automation aimed at one is actively harmful applied to the other. Rewriting a
colleague's branch to fix their lint errors is not a feature. So ownership is
not a display nicety; it is the safety boundary that lets any of the rest exist.

## Determining ownership

Three independent facts can establish that a worktree is owned. They differ in
where they live and how long they are valid, which is what decides whether each
is stored or derived.

### 1. The user created it locally

`lumberjack worktree add` is an unambiguous statement of intent: the user asked
for this branch, on this machine. Nothing in git or `gh` records that fact — a
locally created worktree is indistinguishable from an adopted one after the
fact — so per the "only state that can't be recovered" rule in
`docs/schema.md`, **this must be persisted**.

Add `origin` to `worktrees`:

| value    | meaning                                                        |
| -------- | -------------------------------------------------------------- |
| `local`  | created by `lumberjack worktree add`                            |
| `synced` | created by sync from an open PR                                 |
| `adopted`| found on disk and adopted; provenance unknown                   |

Existing rows migrate to `adopted`, which is the honest answer: we do not know
how they got there, and guessing `local` would silently arm automation against
branches we never created.

### 2. The PR is authored by the logged-in account

Once a PR exists, its author is the authoritative statement of whose change it
is, and it is immutable for the life of the PR — the same property that makes
`github_pr_number` safe to persist on `worktrees`. So `pull_requests` gains
`author_login`, populated from the sync fetch.

`github.PR` gains `AuthorLogin`, and `ListOpenPRs` adds `author` to its
`--json` selection (`gh` returns `author.login`). `FindPRForBranch` adds the
same, so a PR discovered after the fact between sync ticks is classified too.

Ownership is then `author_login == repositories.login` — the `gh` account the
repository was registered under, which the daemon already switches to before
every operation. Comparison is case-insensitive; GitHub logins are not
case-sensitive. A repository with an empty `login` (tracked before that column
existed) cannot evaluate this condition and falls through to condition 3.

The comparison is **derived at read time, not stored**. Both operands are
already in the database, the join is trivial, and persisting the verdict would
create a third copy of a fact that goes stale the moment `lumberjack set-login`
changes the account.

### 3. Every commit on the branch is the user's

This covers the case the other two miss: a branch pushed and adopted, with no
PR yet. If the only commits between the default branch and the branch tip were
made by the user, it is their work.

This is live git state and changes with every push, so it is **derived, never
stored** — `git log <base>..<branch> --format=%ae` against the merge base.

The wrinkle is identity matching: git commits carry an email and name, `gh`
carries a login, and nothing guarantees they agree. Resolve the login to a set
of acceptable commit identities once per repository, cached for the daemon's
lifetime:

- GitHub's noreply form, `<id>+<login>@users.noreply.github.com` and the older
  `<login>@users.noreply.github.com`, which embed the login directly.
- The addresses on the account, from `gh api user/emails --jq '.[].email'`.
- The local `user.email` configured in the main checkout, which is what the
  user's own commits will actually carry in practice.

An empty commit range (branch tip equals the merge base) is **not** owned —
a freshly adopted branch with no commits of its own tells us nothing, and
defaulting to owned arms automation on the strength of no evidence.

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

| field                 | use                                                              |
| --------------------- | ---------------------------------------------------------------- |
| `mergeable`           | `MERGEABLE` / `CONFLICTING` / `UNKNOWN` — conflicts with base     |
| `mergeStateStatus`    | `CLEAN`, `BLOCKED`, `BEHIND`, `DIRTY`, ... — why it can't merge   |
| `reviewDecision`      | `APPROVED` / `CHANGES_REQUESTED` / `REVIEW_REQUIRED`              |
| `statusCheckRollup`   | per-check conclusions — the static analysis signal               |
| `isDraft`             | drafts are never automation candidates                            |

This is the same surface issue #100 proposes as `MergeState`, and it should be
one message rather than two overlapping ones. #100's `base_commit_sha` and
`conflict_count` describe a *local* merge attempt; the fields above describe
GitHub's view. Both belong on the worktree view, and the naming should make
clear which is which — suggest `MergeState` for the local attempt (as #100 has
it) and `PullRequestState` for the GitHub-side rollup.

`GetRepository`, `ListWorktrees` and `Watch` all carry the worktree view, so
all three gain it at once. `Ownership` is an enum on the same message.

Cost is the open problem: this is one `gh pr view` per PR per tick, and issue
#101 already reports sync scaling badly with worktree count. The rollup should
come from a single batched GraphQL query over the repository's open PRs
(`gh api graphql`), not N per-PR calls, and it should be fetched only when a
consumer asks for the view rather than on every sync tick.

## Lifecycle A: owned PRs

The path is worktree → commits → PR → *feedback loop* → merge. The first three
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

Incoming PRs need reviewing, and the question the user actually has is *which
one first*. So each incoming PR gets a rank.

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
result. This is deliberately the *narrow* surface — a single structured
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
   wiring, the three-condition resolver with its git-identity matching, the
   `Ownership` enum on the worktree view, and surfacing in `lumberjack
   worktrees` and the macOS app. No automation. This is the whole safety
   boundary and everything else depends on it.
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

- **Interaction with issue #84 (sync scope).** Ownership is arguably a scope
  dimension too: "sync my PRs only" is a thing people will want. Whether that is
  a fourth scope mode or an orthogonal filter should be settled before #84 lands.
- **Condition 3's cost.** A `git log` per unlinked worktree per tick is cheap
  individually and not free in aggregate. It may need the same
  cache-the-negative-answer treatment the no-PR-for-this-branch lookup already
  gets.
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
