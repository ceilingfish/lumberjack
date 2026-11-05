package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestSyncBatchesMergedLookupAcrossWorktrees(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	for n := int64(1); n <= 6; n++ {
		h.gh.prs = append(h.gh.prs, github.PR{Number: n, HeadBranch: fmt.Sprintf("b%d", n)})
	}
	h.seedSync(t, repo)

	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	byPR := map[int64]string{}
	for _, wt := range wts {
		byPR[*wt.GithubPRNumber] = wt.DirectoryPath
	}
	h.gh.prs = nil
	h.gh.merged = map[int64]bool{1: true, 2: true, 3: true}
	h.git.localOnly[byPR[4]] = 2
	h.git.dirty[byPR[5]] = true
	h.gh.mergedBatches = nil

	var retained []int64
	_, removed, err := h.svc.SyncRepository(context.Background(), repo, func(c WorktreeChange) {
		if c.Action == ActionRetained {
			retained = append(retained, *c.PRNumber)
		}
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 4 {
		t.Errorf("removed = %d, want 4 (three merged and one clean closed)", removed)
	}
	slices.Sort(retained)
	if !slices.Equal(retained, []int64{4, 5}) {
		t.Errorf("retained = %v, want [4 5]", retained)
	}
	if len(h.gh.mergedBatches) != 1 || len(h.gh.mergedBatches[0]) != 6 {
		t.Errorf("merged lookups = %v, want a single batch of 6", h.gh.mergedBatches)
	}
}

func TestSyncUnresolvablePRDoesNotBlockOtherRemovals(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	for n := int64(1); n <= 3; n++ {
		h.gh.prs = append(h.gh.prs, github.PR{Number: n, HeadBranch: fmt.Sprintf("b%d", n)})
	}
	h.seedSync(t, repo)
	h.gh.prs = nil
	h.gh.merged = map[int64]bool{1: true, 2: true}
	h.gh.mergedErrs = map[int64]error{3: errors.New("Could not resolve to a PullRequest")}

	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("sync error = %v, want the PR #3 failure", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
}

func TestWorktreeViewsBatchesMergedLookup(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}, {Number: 2, HeadBranch: "b"}, {Number: 3, HeadBranch: "c"}}
	h.seedSync(t, repo)

	h.gh.prs = []github.PR{{Number: 3, HeadBranch: "c"}}
	h.gh.merged = map[int64]bool{1: true}
	h.gh.mergedBatches = nil

	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	got := map[int64]bool{}
	for _, v := range views {
		got[*v.Worktree.GithubPRNumber] = v.Status.Merged
		if *v.Worktree.GithubPRNumber == 3 && !v.PROpen {
			t.Error("PR 3 should be reported open")
		}
	}
	if want := map[int64]bool{1: true, 2: false, 3: false}; !maps.Equal(got, want) {
		t.Errorf("merged by PR = %v, want %v", got, want)
	}
	if len(h.gh.mergedBatches) != 1 || !slices.Equal(slices.Sorted(slices.Values(h.gh.mergedBatches[0])), []int64{1, 2}) {
		t.Errorf("merged lookups = %v, want one batch of PRs 1 and 2", h.gh.mergedBatches)
	}
}

type alignCase struct {
	h       *harness
	repo    *schema.Repository
	dir     string
	changes []WorktreeChange
}

func newAlignCase(t *testing.T, recorded, checkedOut string, pr *int64) *alignCase {
	t.Helper()
	h := newHarness(t)
	repo := h.repo(t)
	dir := filepath.Join(h.parent, "n-wt")
	if err := mkWorktreeDir(dir); err != nil {
		t.Fatal(err)
	}
	h.seedWorktree(t, repo, recorded, dir, pr)
	h.git.worktrees = []worktree.Ref{
		{Dir: repo.LocalPath, Branch: "main"},
		{Dir: dir, Branch: checkedOut},
	}
	return &alignCase{h: h, repo: repo, dir: dir}
}

func (c *alignCase) sync(t *testing.T) error {
	t.Helper()
	_, _, err := c.h.svc.SyncRepository(context.Background(), c.repo, func(ch WorktreeChange) {
		c.changes = append(c.changes, ch)
	})
	return err
}

func (c *alignCase) recorded(t *testing.T) string {
	t.Helper()
	wts, err := c.h.db.ListWorktrees(context.Background(), c.repo.ID)
	if err != nil || len(wts) != 1 {
		t.Fatalf("ListWorktrees = %v, %v; want one row", wts, err)
	}
	return wts[0].BranchName
}

func (c *alignCase) updated() *WorktreeChange {
	for i := range c.changes {
		if c.changes[i].Action == ActionUpdated && c.changes[i].DirectoryPath == c.dir {
			return &c.changes[i]
		}
	}
	return nil
}

func TestSyncRecordsTheBranchCheckedOutInAnUnlinkedWorktree(t *testing.T) {
	c := newAlignCase(t, "feat/#107-perf", "feat/#101-perf", nil)

	if err := c.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := c.recorded(t); got != "feat/#101-perf" {
		t.Errorf("recorded branch = %q, want feat/#101-perf", got)
	}
	ch := c.updated()
	if ch == nil || ch.Branch != "feat/#101-perf" || ch.Detail != "branch changed from feat/#107-perf" {
		t.Errorf("change = %+v, want an update naming the old branch", ch)
	}
}

func TestSyncLooksUpPRsForTheNewlyRecordedBranch(t *testing.T) {
	c := newAlignCase(t, "feat/old", "feat/new", nil)

	if err := c.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if want := []string{"feat/new"}; !reflect.DeepEqual(c.h.gh.branchLookups, want) {
		t.Errorf("branch lookups = %v, want %v", c.h.gh.branchLookups, want)
	}
}

func TestSyncRecordsPRBranchWhenWorktreeMatchesIt(t *testing.T) {
	num := int64(5)
	c := newAlignCase(t, "feat/stale", "feat/pr", &num)
	c.h.gh.prs = []github.PR{{Number: num, HeadBranch: "feat/pr"}}

	if err := c.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := c.recorded(t); got != "feat/pr" {
		t.Errorf("recorded branch = %q, want feat/pr", got)
	}
}

func TestSyncFailsWhenWorktreeAndPRBranchesDiffer(t *testing.T) {
	num := int64(5)
	c := newAlignCase(t, "feat/pr", "scratch", &num)
	c.h.gh.prs = []github.PR{{Number: num, HeadBranch: "feat/pr"}}

	err := c.sync(t)
	if err == nil || !strings.Contains(err.Error(), "is on branch scratch but PR #5 is on branch feat/pr") {
		t.Fatalf("err = %v, want the branch mismatch named", err)
	}
	if got := c.recorded(t); got != "feat/pr" {
		t.Errorf("recorded branch = %q, want it left at feat/pr", got)
	}
}

func TestSyncLeavesAMatchingWorktreeAlone(t *testing.T) {
	c := newAlignCase(t, "feat/mine", "feat/mine", nil)

	if err := c.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if ch := c.updated(); ch != nil {
		t.Errorf("change = %+v, want none", ch)
	}
}

func TestSyncLeavesDetachedWorktreeRecordAlone(t *testing.T) {
	c := newAlignCase(t, "feat/mine", "", nil)

	if err := c.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := c.recorded(t); got != "feat/mine" {
		t.Errorf("recorded branch = %q, want feat/mine", got)
	}
}

func TestSyncLeavesClosedPRWorktreeRecordAlone(t *testing.T) {
	num := int64(5)
	c := newAlignCase(t, "feat/pr", "scratch", &num)

	if err := c.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := c.recorded(t); got != "feat/pr" {
		t.Errorf("recorded branch = %q, want feat/pr", got)
	}
}

func TestAlignBranchesReportsRecordFailure(t *testing.T) {
	c := newAlignCase(t, "feat/old", "feat/new", nil)
	stored, err := c.h.db.ListWorktrees(context.Background(), c.repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.h.db.Close(); err != nil {
		t.Fatal(err)
	}

	var errs []error
	c.h.svc.alignBranches(context.Background(), c.repo, nil, stored, c.h.git.worktrees, nil, &errs)

	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "recording branch feat/new for worktree") {
		t.Errorf("errs = %v, want the record failure", errs)
	}
	if stored[0].BranchName != "feat/old" {
		t.Errorf("stored branch = %q, want it unchanged on failure", stored[0].BranchName)
	}
}
