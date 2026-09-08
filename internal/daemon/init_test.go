package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/worktree"
)

func TestAdoptExistingWorktreesFailsWhenStoredListingErrors(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}

	if _, err := h.svc.adoptExistingWorktrees(context.Background(), repo); err == nil {
		t.Fatal("adoptExistingWorktrees succeeded with an unusable database")
	}
}

func TestAdoptExistingWorktreesSkipsAlreadyTrackedDirs(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	tracked := filepath.Join(h.parent, "n-tracked")
	fresh := filepath.Join(h.parent, "n-fresh")
	h.seedWorktree(t, repo, "feature/tracked", tracked, nil)
	h.git.worktrees = []worktree.Ref{
		{Dir: tracked, Branch: "feature/tracked"},
		{Dir: fresh, Branch: "feature/fresh"},
	}

	adopted, err := h.svc.adoptExistingWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("adoptExistingWorktrees: %v", err)
	}
	if len(adopted) != 1 || adopted[0].DirectoryPath != fresh {
		t.Fatalf("adopted = %+v, want only %s", adopted, fresh)
	}
}

func TestAdoptExistingWorktreesStopsWhenRecordingFails(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	first := filepath.Join(h.parent, "n-first")
	dup := filepath.Join(h.parent, "n-dup")
	// git reports two branches checked out in the same directory, so the second
	// insert collides on (repository_id, directory_path).
	h.git.worktrees = []worktree.Ref{
		{Dir: first, Branch: "feature/first"},
		{Dir: dup, Branch: "feature/dup"},
		{Dir: dup, Branch: "feature/dup-again"},
	}

	adopted, err := h.svc.adoptExistingWorktrees(context.Background(), repo)
	if err == nil {
		t.Fatal("adoptExistingWorktrees succeeded despite the directory collision")
	}
	if !strings.Contains(err.Error(), "recording adopted worktree "+dup) {
		t.Errorf("error = %v, want it to name the colliding directory", err)
	}
	if len(adopted) != 2 {
		t.Errorf("adopted = %+v, want the two rows stored before the failure", adopted)
	}
}
