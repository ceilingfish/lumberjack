package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReconcileClean(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	if err := g.AddWorktree(ctx, main, dir, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	st, err := Reconcile(ctx, g, dir, PROpen)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if st.NeedsReconciliation || st.Orphaned || st.Note != "" {
		t.Errorf("clean+open worktree: %+v", st)
	}
}

func TestReconcileDirtyOpen(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	_ = g.AddWorktree(ctx, main, dir, "origin", "feature/foo")
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, _ := Reconcile(ctx, g, dir, PROpen)
	if !st.Dirty || !st.NeedsReconciliation {
		t.Errorf("expected dirty+needs reconciliation: %+v", st)
	}
	if st.Orphaned {
		t.Error("open PR should not be orphaned")
	}
}

func TestReconcileOrphaned(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	_ = g.AddWorktree(ctx, main, dir, "origin", "feature/foo")
	// A local-only commit, and the PR is closed (prOpen=false) → orphaned.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "local")

	st, _ := Reconcile(ctx, g, dir, PRGone)
	if !st.Orphaned || st.LocalOnlyCommits != 1 {
		t.Errorf("expected orphaned with 1 local commit: %+v", st)
	}
	if st.Note == "" {
		t.Error("expected a reconciliation note")
	}
}

// TestReconcileMergedIgnoresLocalOnlyCommits covers the squash-merge false
// positive: a branch merged into the base branch keeps commits that live on no
// remote-tracking ref, but they are not at risk, so a merged worktree is a
// safe-remove candidate rather than an orphan.
func TestReconcileMerged(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	_ = g.AddWorktree(ctx, main, dir, "origin", "feature/foo")
	// Commits that exist on no remote ref (as after a squash merge deletes the
	// head branch) would count as local-only, but the PR is merged.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "merged work")

	st, _ := Reconcile(ctx, g, dir, PRMerged)
	if st.NeedsReconciliation || st.Orphaned || st.LocalOnlyCommits != 0 {
		t.Errorf("merged worktree should be a safe-remove candidate: %+v", st)
	}
	if !st.Merged || st.Note != "PR merged; safe to remove" {
		t.Errorf("note = %q, merged = %v", st.Note, st.Merged)
	}
}

// A merged PR whose worktree still has uncommitted changes is kept — the merge
// covers committed work, not the dirty tree.
func TestReconcileMergedButDirty(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	_ = g.AddWorktree(ctx, main, dir, "origin", "feature/foo")
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, _ := Reconcile(ctx, g, dir, PRMerged)
	if !st.Dirty || !st.NeedsReconciliation || !st.Merged {
		t.Errorf("merged+dirty should still need reconciliation: %+v", st)
	}
	if st.Note != "PR merged but uncommitted changes" {
		t.Errorf("note = %q", st.Note)
	}
}

func TestReconcileClosedButClean(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	_ = g.AddWorktree(ctx, main, dir, "origin", "feature/foo")

	st, _ := Reconcile(ctx, g, dir, PRGone)
	if st.NeedsReconciliation || st.Orphaned {
		t.Errorf("clean closed worktree is a safe-remove candidate: %+v", st)
	}
	if st.Note != "PR closed; safe to remove" {
		t.Errorf("note = %q", st.Note)
	}
}

func TestReconcileMissingDir(t *testing.T) {
	g, main := setupRepos(t)
	st, err := Reconcile(context.Background(), g, filepath.Join(filepath.Dir(main), "gone"), PROpen)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !st.Missing {
		t.Errorf("expected Missing for absent dir: %+v", st)
	}
}
