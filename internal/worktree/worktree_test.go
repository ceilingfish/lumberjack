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

	st, err := Reconcile(ctx, g, dir, true)
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

	st, _ := Reconcile(ctx, g, dir, true)
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

	st, _ := Reconcile(ctx, g, dir, false)
	if !st.Orphaned || st.LocalOnlyCommits != 1 {
		t.Errorf("expected orphaned with 1 local commit: %+v", st)
	}
	if st.Note == "" {
		t.Error("expected a reconciliation note")
	}
}

func TestReconcileClosedButClean(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	_ = g.Fetch(ctx, main, "origin")

	dir := filepath.Join(filepath.Dir(main), "wt")
	_ = g.AddWorktree(ctx, main, dir, "origin", "feature/foo")

	st, _ := Reconcile(ctx, g, dir, false)
	if st.NeedsReconciliation || st.Orphaned {
		t.Errorf("clean closed worktree is a safe-remove candidate: %+v", st)
	}
	if st.Note != "PR closed; safe to remove" {
		t.Errorf("note = %q", st.Note)
	}
}

func TestReconcileMissingDir(t *testing.T) {
	g, main := setupRepos(t)
	st, err := Reconcile(context.Background(), g, filepath.Join(filepath.Dir(main), "gone"), true)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !st.Missing {
		t.Errorf("expected Missing for absent dir: %+v", st)
	}
}
