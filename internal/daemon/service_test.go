package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
)

// fakeGit satisfies GitOps against real temp directories so worktree.Reconcile
// (which stats the directory) works, while dirt and local-commit state are
// answered from in-memory maps keyed by directory.
type fakeGit struct {
	dirty     map[string]bool
	localOnly map[string]int64
	addErr    map[string]error // keyed by branch
	fetchErr  error
	remotes   string
	remoteErr error
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		dirty:     map[string]bool{},
		localOnly: map[string]int64{},
		addErr:    map[string]error{},
		remotes:   "origin",
	}
}

func (f *fakeGit) DefaultRemote(context.Context, string) (string, error) {
	if f.remoteErr != nil {
		return "", f.remoteErr
	}
	return f.remotes, nil
}
func (f *fakeGit) Fetch(context.Context, string, string) error { return f.fetchErr }

func (f *fakeGit) AddWorktree(_ context.Context, _, dir, _, branch string) error {
	if err := f.addErr[branch]; err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func (f *fakeGit) RemoveWorktree(_ context.Context, _, dir string, _ bool) error {
	return os.RemoveAll(dir)
}

func (f *fakeGit) IsDirty(_ context.Context, dir string) (bool, error) {
	return f.dirty[dir], nil
}

func (f *fakeGit) LocalOnlyCommits(_ context.Context, dir string) (int64, error) {
	return f.localOnly[dir], nil
}

// fakeGH satisfies GHOps.
type fakeGH struct {
	info    github.RepoInfo
	infoErr error
	prs     []github.PR
	prsErr  error
}

func (f *fakeGH) RepoInfo(context.Context, string) (github.RepoInfo, error) {
	return f.info, f.infoErr
}

func (f *fakeGH) ListOpenPRs(context.Context, github.RepoInfo) ([]github.PR, error) {
	return f.prs, f.prsErr
}

// harness bundles a Service over a temp DB with controllable fakes.
type harness struct {
	svc    *Service
	db     *database.Client
	git    *fakeGit
	gh     *fakeGH
	parent string // worktree parent dir (a temp dir)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("Open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	git := newFakeGit()
	gh := &fakeGH{info: github.RepoInfo{Owner: "o", Name: "n", Host: "github.com", DefaultBranch: "main"}}
	return &harness{
		svc:    NewService(db, git, gh),
		db:     db,
		git:    git,
		gh:     gh,
		parent: t.TempDir(),
	}
}

// repo inserts and returns a tracked repository rooted under the temp parent.
func (h *harness) repo(t *testing.T) *schema.Repository {
	t.Helper()
	r := &schema.Repository{
		LocalPath: filepath.Join(h.parent, "n"), WorktreeParentDir: h.parent,
		DirPrefix: "n", GithubOwner: "o", GithubName: "n",
		DefaultRemote: "origin", Host: "github.com",
	}
	if err := h.db.CreateRepository(context.Background(), r); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	return r
}

func TestInitRepository(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.parent, "myrepo")

	repo, err := h.svc.InitRepository(context.Background(), dir)
	if err != nil {
		t.Fatalf("InitRepository: %v", err)
	}
	if repo.DirPrefix != "myrepo" || repo.WorktreeParentDir != h.parent {
		t.Errorf("derived defaults wrong: %+v", repo)
	}
	if repo.GithubOwner != "o" || repo.Host != "github.com" || repo.DefaultRemote != "origin" {
		t.Errorf("gh/git identity wrong: %+v", repo)
	}
}

func TestInitRepositoryNotGitHub(t *testing.T) {
	h := newHarness(t)
	h.gh.infoErr = errors.New("not a repo")
	if _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x")); err == nil {
		t.Error("expected error for non-GitHub repo")
	}
}

func TestInitRepositoryNoRemote(t *testing.T) {
	h := newHarness(t)
	h.git.remoteErr = errors.New("no remotes")
	if _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x")); err == nil {
		t.Error("expected error when repo has no remote")
	}
}

func TestInitRepositoryDuplicate(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.parent, "dup")
	if _, err := h.svc.InitRepository(context.Background(), dir); err != nil {
		t.Fatalf("first init: %v", err)
	}
	_, err := h.svc.InitRepository(context.Background(), dir)
	if !errors.Is(err, database.ErrRepositoryExists) {
		t.Errorf("expected ErrRepositoryExists, got %v", err)
	}
}

func TestSyncCreatesWorktrees(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}, {Number: 2, HeadBranch: "b"}}

	var msgs []string
	created, removed, err := h.svc.SyncRepository(context.Background(), repo, func(m string) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if created != 2 || removed != 0 {
		t.Errorf("created=%d removed=%d, want 2/0", created, removed)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(wts))
	}
	// Slug applied: feature/a -> n-a.
	found := false
	for _, wt := range wts {
		if filepath.Base(wt.DirectoryPath) == "n-a" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected worktree dir n-a, got %+v", wts)
	}
	if len(msgs) == 0 {
		t.Error("expected progress messages")
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if created != 0 {
		t.Errorf("second sync created %d, want 0", created)
	}
}

func TestSyncRemovesClosedCleanWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

	// PR #1 closes: clean worktree should be removed.
	h.gh.prs = nil
	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed=%d, want 1", removed)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 0 {
		t.Errorf("expected worktree removed, got %d", len(wts))
	}
}

func TestSyncRetainsDirtyClosedWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	// Mark the worktree dirty and its PR closed.
	h.git.dirty[wts[0].DirectoryPath] = true
	h.gh.prs = nil

	var msgs []string
	_, removed, err := h.svc.SyncRepository(context.Background(), repo, func(m string) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0 (retained)", removed)
	}
	if got, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(got) != 1 {
		t.Errorf("expected worktree retained, got %d", len(got))
	}
}

func TestSyncPrunesMissingWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	_ = os.RemoveAll(wts[0].DirectoryPath) // directory vanishes by hand
	h.gh.prs = nil

	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed=%d, want 1 (pruned)", removed)
	}
}

func TestSyncAddWorktreeErrorIsCollected(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "good"}, {Number: 2, HeadBranch: "bad"}}
	h.git.addErr["bad"] = errors.New("fork branch not on origin")

	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil {
		t.Error("expected a combined error for the failing PR")
	}
	if created != 1 {
		t.Errorf("created=%d, want 1 (the good PR)", created)
	}
	// The failure must be recorded on the repo's sync status.
	got, _ := h.db.FindRepository(context.Background(), repo.LocalPath)
	if got.LastSyncStatus == nil || *got.LastSyncStatus != schema.SyncStatusError {
		t.Errorf("expected error sync status, got %v", got.LastSyncStatus)
	}
}

func TestSyncSlugCollisionDisambiguates(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// Two branches that slug identically must get distinct directories.
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/x"}, {Number: 2, HeadBranch: "x"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	dirs := map[string]bool{}
	for _, wt := range wts {
		dirs[wt.DirectoryPath] = true
	}
	if len(dirs) != 2 {
		t.Errorf("expected 2 distinct dirs, got %v", dirs)
	}
}

func TestSyncFetchErrorAborts(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.fetchErr = errors.New("network down")
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err == nil {
		t.Error("expected fetch error to abort sync")
	}
}

func TestWorktreeViews(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	if len(views) != 1 || !views[0].PROpen || views[0].Status.NeedsReconciliation {
		t.Errorf("unexpected view: %+v", views)
	}
}

func TestDeleteWorktreeClean(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

	res, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !res.Deleted || res.RequiresConfirmation {
		t.Errorf("clean delete: %+v", res)
	}
}

func TestDeleteWorktreeNeedsConfirmation(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	h.git.localOnly[wts[0].DirectoryPath] = 3

	// Unforced: must ask for confirmation and report commits at risk.
	res, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !res.RequiresConfirmation || res.CommitsAtRisk != 3 || res.Deleted {
		t.Errorf("expected confirmation with 3 commits: %+v", res)
	}

	// Forced: deletes despite the risk.
	res, err = h.svc.DeleteWorktree(context.Background(), repo, "a", true)
	if err != nil {
		t.Fatalf("forced DeleteWorktree: %v", err)
	}
	if !res.Deleted {
		t.Errorf("forced delete should succeed: %+v", res)
	}
}

func TestDeleteWorktreeNotFound(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if _, err := h.svc.DeleteWorktree(context.Background(), repo, "ghost", false); !errors.Is(err, database.ErrWorktreeNotFound) {
		t.Errorf("expected ErrWorktreeNotFound, got %v", err)
	}
}

func TestDeleteWorktreeMissingDir(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	_ = os.RemoveAll(wts[0].DirectoryPath)

	res, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !res.Deleted {
		t.Errorf("missing-dir delete should prune tracking: %+v", res)
	}
}

// ensure fakeGit satisfies GitOps and time is injectable.
var _ GitOps = (*fakeGit)(nil)

func TestNowInjectable(t *testing.T) {
	h := newHarness(t)
	fixed := time.Unix(1000, 0)
	h.svc.now = func() time.Time { return fixed }
	repo := h.repo(t)
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)
	run, _ := h.db.LatestSyncRun(context.Background(), repo.ID)
	if run == nil || !run.StartedAt.Equal(fixed) {
		t.Errorf("expected injected time, got %+v", run)
	}
}
