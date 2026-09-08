package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// seedWorktree stores a worktree row directly, bypassing sync, so a test can
// arrange the collisions that make a later insert or update fail.
func (h *harness) seedWorktree(t *testing.T, repo *schema.Repository, branch, dir string, pr *int64) schema.Worktree {
	t.Helper()
	row := &schema.Worktree{
		RepositoryID: repo.ID, GithubPRNumber: pr,
		BranchName: branch, DirectoryPath: dir,
	}
	if err := h.db.CreateWorktree(context.Background(), row); err != nil {
		t.Fatalf("seed CreateWorktree: %v", err)
	}
	return *row
}

func TestLinkWorktreeReportsUpdateFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	num := int64(7)
	wt := h.seedWorktree(t, repo, "feature/free", filepath.Join(h.parent, "n-free"), nil)
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}

	var errs []error
	h.svc.linkWorktree(context.Background(), repo, num, github.PR{Number: num, HeadBranch: "feature/free"}, wt, nil, &errs)

	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}
	if !strings.Contains(errs[0].Error(), "linking PR #7 to worktree") {
		t.Errorf("error = %v, want it to name the PR and worktree", errs[0])
	}
}

func TestAdoptWorktreeReportsRecordFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	dir := filepath.Join(h.parent, "n-adopted")
	h.seedWorktree(t, repo, "feature/other", dir, nil)

	var errs []error
	ok := h.svc.adoptWorktree(context.Background(), repo, nil, "feature/adopted", dir, nil, &errs)

	if ok {
		t.Error("adoptWorktree = true, want false when the row could not be stored")
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "recording adopted worktree "+dir) {
		t.Errorf("errs = %v, want one naming the directory", errs)
	}
}

func TestCreateWorktreeRollsBackDirWhenRecordingFails(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	dir := filepath.Join(h.parent, "n-pr")
	h.seedWorktree(t, repo, "feature/other", dir, nil)

	var errs []error
	got, ok := h.svc.createWorktree(
		context.Background(), repo, 9,
		github.PR{Number: 9, HeadBranch: "feature/pr"}, map[string]bool{}, nil, &errs,
	)

	if ok || got != "" {
		t.Errorf("createWorktree = (%q, %v), want (\"\", false)", got, ok)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "recording worktree for PR #9") {
		t.Errorf("errs = %v, want one naming the PR", errs)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("worktree directory survived the rollback: %v", statErr)
	}
}

func TestIndexRefsSkipsDetachedAndKeepsFirstDirPerBranch(t *testing.T) {
	branchByDir, dirByBranch := indexRefs([]worktree.Ref{
		{Dir: "/a", Branch: "main"},
		{Dir: "/b", Branch: ""},
		{Dir: "/c", Branch: "main"},
	})

	wantByDir := map[string]string{"/a": "main", "/b": "", "/c": "main"}
	if !reflect.DeepEqual(branchByDir, wantByDir) {
		t.Errorf("branchByDir = %v, want %v", branchByDir, wantByDir)
	}
	wantByBranch := map[string]string{"main": "/a"}
	if !reflect.DeepEqual(dirByBranch, wantByBranch) {
		t.Errorf("dirByBranch = %v, want %v (a detached ref indexes no branch)", dirByBranch, wantByBranch)
	}
}

func TestPrBranchOfIsEmptyWithoutAPR(t *testing.T) {
	if got := prBranchOf(schema.Worktree{BranchName: "feature/a"}); got != "" {
		t.Errorf("prBranchOf = %q, want empty for a worktree no PR claims", got)
	}
	num := int64(3)
	if got := prBranchOf(schema.Worktree{BranchName: "feature/a", GithubPRNumber: &num}); got != "feature/a" {
		t.Errorf("prBranchOf = %q, want feature/a", got)
	}
}
