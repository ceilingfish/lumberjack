package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// PRState is the state of a worktree's source pull request as far as
// reconciliation cares. A merged PR is treated very differently from a closed
// one: its branch commits are already on the base branch, so they are not local
// work at risk (this is what fixes the squash-merge false positive — a
// squash-merged branch's original commits are reachable from no remote ref, so
// LocalOnlyCommits alone would miscount them as unpushed work).
type PRState int

const (
	// PRGone means the PR was closed without being merged — any local-only
	// commits are genuinely at risk.
	PRGone PRState = iota
	// PROpen means the PR is still open — a healthy tracked worktree.
	PROpen
	// PRMerged means the PR was merged into the base branch, so the branch's
	// commits are preserved there regardless of what LocalOnlyCommits reports.
	PRMerged
	// PRNone means the worktree has no associated PR (a tracked branch that has
	// not opened one, or one not yet linked). It is never a removal candidate:
	// without a finished PR there is nothing to say the work is safe to discard.
	PRNone
)

// Status is the live reconciliation view of a worktree, computed fresh from
// git (never persisted — see docs/schema.md, "Derived live, not stored"). It
// maps directly onto the derived fields of the lumberjack.v1.Worktree message.
type Status struct {
	// Dirty is true when the working tree has uncommitted changes.
	Dirty bool
	// LocalOnlyCommits counts commits present locally but on no remote — work
	// that would be lost if the worktree were removed. It is forced to zero for
	// a merged PR, whose commits are safely on the base branch.
	LocalOnlyCommits int64
	// Merged is true when the source PR was merged; its commits are on the base
	// branch, so the worktree is a safe-remove candidate even though its branch
	// tip sits on no remote-tracking ref.
	Merged bool
	// NeedsReconciliation is true when the worktree cannot be safely
	// auto-removed: it is dirty or holds local-only commits.
	NeedsReconciliation bool
	// Orphaned is true when the source PR is gone (merged/closed) but the
	// worktree was retained because it still needs reconciliation.
	Orphaned bool
	// Note is a human-readable summary; empty when fully in sync.
	Note string
	// Missing is true when the worktree directory is gone from disk (e.g.
	// removed by hand). Such a worktree can be pruned from tracking safely.
	Missing bool
}

// Prober is the subset of *Git that Reconcile needs. Defining it as an
// interface lets the daemon's sync engine be unit-tested with a fake git.
type Prober interface {
	IsDirty(ctx context.Context, dir string) (bool, error)
	LocalOnlyCommits(ctx context.Context, dir string) (int64, error)
}

// Reconcile computes the live status of the worktree at dir. pr is the state of
// the worktree's source PR (the daemon supplies this from a live gh query); it
// distinguishes a healthy tracked worktree, an orphaned one that outlived a
// closed PR, and one whose PR merged (whose commits are safe on the base
// branch).
//
// p must have fetched the repo first so remote-tracking refs are current.
func Reconcile(ctx context.Context, p Prober, dir string, pr PRState) (Status, error) {
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		// Genuinely gone from disk — safe to prune from tracking.
		return Status{Missing: true, Note: "worktree directory is missing"}, nil
	case err != nil:
		// A permission or transient I/O error is not the same as missing; do
		// not let it masquerade as a prunable worktree.
		return Status{}, fmt.Errorf("stat worktree directory: %w", err)
	case !info.IsDir():
		return Status{Missing: true, Note: "worktree path is not a directory"}, nil
	}

	// A worktree directory always contains a .git entry (a gitdir pointer
	// file). Its absence means the worktree was removed out-of-band and only a
	// husk remains — typically ignored build artifacts that survived `git
	// worktree remove`. Probing it with git would fail ("not a git
	// repository"), so treat it as missing: prunable from tracking, with the
	// husk left on disk for the user.
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		return Status{Missing: true, Note: "directory is no longer a git worktree"}, nil
	}

	dirty, err := p.IsDirty(ctx, dir)
	if err != nil {
		return Status{}, fmt.Errorf("checking dirty state: %w", err)
	}
	localOnly, err := p.LocalOnlyCommits(ctx, dir)
	if err != nil {
		return Status{}, fmt.Errorf("counting local-only commits: %w", err)
	}

	st := Status{Dirty: dirty, LocalOnlyCommits: localOnly, Merged: pr == PRMerged}
	if st.Merged {
		// A merged PR's commits live on the base branch, so they are not local
		// work at risk — only uncommitted changes still warrant keeping the tree.
		st.LocalOnlyCommits = 0
	}
	st.NeedsReconciliation = st.Dirty || st.LocalOnlyCommits > 0
	st.Orphaned = pr == PRGone && st.NeedsReconciliation
	st.Note = note(st, pr)
	return st, nil
}

// note renders a human-readable one-liner for a Status.
func note(st Status, pr PRState) string {
	switch {
	case !st.NeedsReconciliation && (pr == PROpen || pr == PRNone):
		return ""
	case !st.NeedsReconciliation && pr == PRMerged:
		return "PR merged; safe to remove"
	case !st.NeedsReconciliation:
		// PR gone and nothing to preserve — a clean removal candidate.
		return "PR closed; safe to remove"
	case st.Merged:
		// Merged but the working tree still has uncommitted changes to resolve.
		return "PR merged but " + reason(st)
	case st.Orphaned:
		return "orphaned: PR closed but " + reason(st)
	default:
		return "needs reconciliation: " + reason(st)
	}
}

// reason describes why a worktree needs reconciliation.
func reason(st Status) string {
	switch {
	case st.Dirty && st.LocalOnlyCommits > 0:
		return fmt.Sprintf("uncommitted changes and %d local-only commit(s)", st.LocalOnlyCommits)
	case st.Dirty:
		return "uncommitted changes"
	default:
		return fmt.Sprintf("%d local-only commit(s)", st.LocalOnlyCommits)
	}
}
