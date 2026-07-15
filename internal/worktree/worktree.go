package worktree

import (
	"context"
	"fmt"
	"os"
)

// Status is the live reconciliation view of a worktree, computed fresh from
// git (never persisted — see docs/schema.md, "Derived live, not stored"). It
// maps directly onto the derived fields of the lumberjack.v1.Worktree message.
type Status struct {
	// Dirty is true when the working tree has uncommitted changes.
	Dirty bool
	// LocalOnlyCommits counts commits present locally but on no remote.
	LocalOnlyCommits int64
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

// Reconcile computes the live status of the worktree at dir. prOpen reports
// whether the worktree's source PR is still open (the daemon supplies this
// from a live gh query); it distinguishes a healthy tracked worktree from an
// orphaned one that outlived its PR.
//
// p must have fetched the repo first so remote-tracking refs are current.
func Reconcile(ctx context.Context, p Prober, dir string, prOpen bool) (Status, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Status{Missing: true, Note: "worktree directory is missing"}, nil
	}

	dirty, err := p.IsDirty(ctx, dir)
	if err != nil {
		return Status{}, fmt.Errorf("checking dirty state: %w", err)
	}
	localOnly, err := p.LocalOnlyCommits(ctx, dir)
	if err != nil {
		return Status{}, fmt.Errorf("counting local-only commits: %w", err)
	}

	st := Status{
		Dirty:               dirty,
		LocalOnlyCommits:    localOnly,
		NeedsReconciliation: dirty || localOnly > 0,
	}
	st.Orphaned = !prOpen && st.NeedsReconciliation
	st.Note = note(st, prOpen)
	return st, nil
}

// note renders a human-readable one-liner for a Status.
func note(st Status, prOpen bool) string {
	switch {
	case !st.NeedsReconciliation && prOpen:
		return ""
	case !st.NeedsReconciliation && !prOpen:
		// PR gone and nothing to preserve — a clean removal candidate.
		return "PR closed; safe to remove"
	case st.Orphaned:
		return fmt.Sprintf("orphaned: PR closed but %s", reason(st))
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
