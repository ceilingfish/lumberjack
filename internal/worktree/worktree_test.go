package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// A directory that exists but holds no .git entry is a husk left behind by an
// out-of-band removal (e.g. ignored build artifacts that survived `git
// worktree remove`). It must reconcile as Missing rather than fail the sync
// with "not a git repository".
func TestReconcileHuskDir(t *testing.T) {
	g, main := setupRepos(t)
	dir := filepath.Join(filepath.Dir(main), "husk")
	if err := os.MkdirAll(filepath.Join(dir, ".next"), 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := Reconcile(context.Background(), g, dir, PROpen)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !st.Missing || st.Note != "directory is no longer a git worktree" {
		t.Errorf("expected Missing husk: %+v", st)
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

type fakeProber struct {
	dirty     bool
	localOnly int64
	dirtyErr  error
	countErr  error
}

func (f fakeProber) IsDirty(context.Context, string) (bool, error) {
	return f.dirty, f.dirtyErr
}

func (f fakeProber) LocalOnlyCommits(context.Context, string) (int64, error) {
	return f.localOnly, f.countErr
}

func fakeWorktreeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReconcileDecisions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		probe fakeProber
		pr    PRState
		want  Status
	}{
		{
			name: "clean with no PR is in sync",
			pr:   PRNone,
			want: Status{},
		},
		{
			name:  "dirty with no PR is never a removal candidate",
			probe: fakeProber{dirty: true},
			pr:    PRNone,
			want: Status{
				Dirty:               true,
				NeedsReconciliation: true,
				Note:                "needs reconciliation: uncommitted changes",
			},
		},
		{
			name:  "unpushed commits on an open PR",
			probe: fakeProber{localOnly: 3},
			pr:    PROpen,
			want: Status{
				LocalOnlyCommits:    3,
				NeedsReconciliation: true,
				Note:                "needs reconciliation: 3 local-only commit(s)",
			},
		},
		{
			name:  "orphaned with both kinds of unsaved work",
			probe: fakeProber{dirty: true, localOnly: 2},
			pr:    PRGone,
			want: Status{
				Dirty:               true,
				LocalOnlyCommits:    2,
				NeedsReconciliation: true,
				Orphaned:            true,
				Note:                "orphaned: PR closed but uncommitted changes and 2 local-only commit(s)",
			},
		},
		{
			name:  "merged discards the local-only count",
			probe: fakeProber{localOnly: 5},
			pr:    PRMerged,
			want: Status{
				Merged: true,
				Note:   "PR merged; safe to remove",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Reconcile(context.Background(), tc.probe, fakeWorktreeDir(t), tc.pr)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if got != tc.want {
				t.Errorf("Reconcile = %+v, want %+v", got, tc.want)
			}
		})
	}
}

var errProbe = errors.New("git exploded")

func TestReconcileProbeErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		probe   fakeProber
		wantErr string
	}{
		{"dirty check fails", fakeProber{dirtyErr: errProbe}, "checking dirty state"},
		{"commit count fails", fakeProber{countErr: errProbe}, "counting local-only commits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Reconcile(context.Background(), tc.probe, fakeWorktreeDir(t), PROpen)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Reconcile error = %v, want one containing %q", err, tc.wantErr)
			}
			if !errors.Is(err, errProbe) {
				t.Errorf("Reconcile error %v does not wrap the probe failure", err)
			}
			if st != (Status{}) {
				t.Errorf("Reconcile = %+v on failure, want the zero Status", st)
			}
		})
	}
}

func TestReconcileNotADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := Reconcile(context.Background(), fakeProber{}, path, PROpen)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !st.Missing || st.Note != "worktree path is not a directory" {
		t.Errorf("Reconcile = %+v, want Missing", st)
	}
}

func TestReconcileStatError(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := Reconcile(context.Background(), fakeProber{}, filepath.Join(notADir, "under-a-file"), PROpen)
	if err == nil {
		t.Fatal("Reconcile: expected an error")
	}
	if !strings.Contains(err.Error(), "stat worktree directory") {
		t.Errorf("error %q does not say what failed", err)
	}
	if st.Missing {
		t.Error("a stat failure was reported as a missing worktree")
	}
}
