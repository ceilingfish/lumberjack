package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// repositoryByID and listPRs are read-back helpers used only by these store
// tests to assert what the write methods persisted. They live here rather than
// in the production Client because nothing outside the tests reads by id or
// lists tracked PRs.
func (c *Client) repositoryByID(ctx context.Context, id int64) (*schema.Repository, error) {
	repo := new(schema.Repository)
	if err := c.NewSelect().Model(repo).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRepositoryNotFound
		}
		return nil, err
	}
	return repo, nil
}

func (c *Client) listPRs(ctx context.Context, repoID int64) ([]schema.PullRequest, error) {
	var prs []schema.PullRequest
	if err := c.NewSelect().Model(&prs).
		Where("repository_id = ?", repoID).Order("github_pr_number ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing pull requests: %w", err)
	}
	return prs, nil
}

func newRepo(localPath, prefix, name string) *schema.Repository {
	return &schema.Repository{
		LocalPath: localPath, WorktreeParentDir: "/parent", DirPrefix: prefix,
		GithubOwner: "o", GithubName: name, DefaultRemote: "origin", Host: "github.com",
	}
}

func TestCreateAndListRepositories(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()

	if err := c.CreateRepository(ctx, newRepo("/a", "a", "A")); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	if err := c.CreateRepository(ctx, newRepo("/b", "b", "B")); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	repos, err := c.ListRepositories(ctx)
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
}

func TestRepositoryLoginRoundTrips(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()

	r := newRepo("/a", "a", "A")
	r.Login = "work-account"
	if err := c.CreateRepository(ctx, r); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	got, err := c.FindRepository(ctx, "/a")
	if err != nil {
		t.Fatalf("FindRepository: %v", err)
	}
	if got.Login != "work-account" {
		t.Errorf("Login = %q, want work-account", got.Login)
	}
}

func TestUpdateLogin(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	if err := c.CreateRepository(ctx, repo); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if err := c.UpdateLogin(ctx, repo.ID, "work-account"); err != nil {
		t.Fatalf("UpdateLogin: %v", err)
	}
	got, _ := c.repositoryByID(ctx, repo.ID)
	if got.Login != "work-account" {
		t.Errorf("Login = %q, want work-account", got.Login)
	}

	// A second call overwrites the previous login.
	if err := c.UpdateLogin(ctx, repo.ID, "personal"); err != nil {
		t.Fatalf("UpdateLogin: %v", err)
	}
	got, _ = c.repositoryByID(ctx, repo.ID)
	if got.Login != "personal" {
		t.Errorf("Login = %q, want personal", got.Login)
	}
}

func TestCreateRepositoryDuplicate(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	if err := c.CreateRepository(ctx, newRepo("/a", "a", "A")); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	err := c.CreateRepository(ctx, newRepo("/a", "a2", "A2"))
	if !errors.Is(err, ErrRepositoryExists) {
		t.Errorf("expected ErrRepositoryExists, got %v", err)
	}
}

func TestFindRepository(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	_ = c.CreateRepository(ctx, newRepo("/path/to/repo", "repo", "RepoName"))

	for _, ref := range []string{"/path/to/repo", "repo", "RepoName"} {
		got, err := c.FindRepository(ctx, ref)
		if err != nil {
			t.Fatalf("FindRepository(%q): %v", ref, err)
		}
		if got.LocalPath != "/path/to/repo" {
			t.Errorf("FindRepository(%q) = %q", ref, got.LocalPath)
		}
	}

	if _, err := c.FindRepository(ctx, "nope"); !errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("expected ErrRepositoryNotFound, got %v", err)
	}
}

func TestFindRepositoryPrefersLocalPath(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	// One repo's dir_prefix collides with another's local path lookup key.
	_ = c.CreateRepository(ctx, newRepo("/x", "shared", "X"))
	_ = c.CreateRepository(ctx, newRepo("shared", "y", "Y"))

	got, err := c.FindRepository(ctx, "shared")
	if err != nil {
		t.Fatalf("FindRepository: %v", err)
	}
	// The exact local-path match ("shared" == local_path of Y) wins.
	if got.LocalPath != "shared" {
		t.Errorf("expected local-path match to win, got %q", got.LocalPath)
	}
}

func TestFindRepositoryFromEnclosingPath(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/code/repo", "repo", "RepoName")
	_ = c.CreateRepository(ctx, repo)
	_ = c.CreateWorktree(ctx, &schema.Worktree{
		RepositoryID: repo.ID, BranchName: "feature/x", DirectoryPath: "/code/repo-feature-x",
	})

	// A tracked worktree, a subdirectory of one, and a subdirectory of the main
	// checkout all resolve to the owning repository — so the scoped commands
	// work from anywhere inside a tracked tree.
	for _, ref := range []string{
		"/code/repo-feature-x",
		"/code/repo-feature-x/internal/database",
		"/code/repo/cmd",
	} {
		got, err := c.FindRepository(ctx, ref)
		if err != nil {
			t.Fatalf("FindRepository(%q): %v", ref, err)
		}
		if got.ID != repo.ID {
			t.Errorf("FindRepository(%q) = %q, want %q", ref, got.LocalPath, repo.LocalPath)
		}
	}

	// A path outside every tracked tree still reports not found rather than
	// walking up to an unrelated repository.
	if _, err := c.FindRepository(ctx, "/elsewhere/thing"); !errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("expected ErrRepositoryNotFound, got %v", err)
	}
}

func TestUpdateSyncResult(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)

	now := time.Now()
	if err := c.UpdateSyncResult(ctx, repo.ID, now, nil); err != nil {
		t.Fatalf("UpdateSyncResult: %v", err)
	}
	got, _ := c.repositoryByID(ctx, repo.ID)
	if got.LastSyncStatus == nil || *got.LastSyncStatus != schema.SyncStatusOK {
		t.Errorf("status = %v, want ok", got.LastSyncStatus)
	}

	if err := c.UpdateSyncResult(ctx, repo.ID, now, errors.New("boom")); err != nil {
		t.Fatalf("UpdateSyncResult(err): %v", err)
	}
	got, _ = c.repositoryByID(ctx, repo.ID)
	if got.LastSyncStatus == nil || *got.LastSyncStatus != schema.SyncStatusError {
		t.Errorf("status = %v, want error", got.LastSyncStatus)
	}
	if got.LastSyncError == nil || *got.LastSyncError != "boom" {
		t.Errorf("error = %v", got.LastSyncError)
	}
}

func TestRepositoryByIDMissing(t *testing.T) {
	c := openTemp(t)
	if _, err := c.repositoryByID(context.Background(), 999); !errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("expected ErrRepositoryNotFound, got %v", err)
	}
}

func TestWorktreeStore(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)

	num := int64(42)
	wt := &schema.Worktree{
		RepositoryID: repo.ID, GithubPRNumber: &num,
		BranchName: "feature/x", DirectoryPath: "/parent/a-x",
	}
	if err := c.CreateWorktree(ctx, wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	list, err := c.ListWorktrees(ctx, repo.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListWorktrees = %v, %v", list, err)
	}

	// Resolvable by branch, full path, and base name.
	for _, ref := range []string{"feature/x", "/parent/a-x", "a-x"} {
		if _, err := c.FindWorktree(ctx, repo.ID, ref); err != nil {
			t.Errorf("FindWorktree(%q): %v", ref, err)
		}
	}
	if _, err := c.FindWorktree(ctx, repo.ID, "missing"); !errors.Is(err, ErrWorktreeNotFound) {
		t.Errorf("expected ErrWorktreeNotFound, got %v", err)
	}

	if err := c.DeleteWorktree(ctx, wt.ID); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	list, _ = c.ListWorktrees(ctx, repo.ID)
	if len(list) != 0 {
		t.Errorf("expected 0 worktrees after delete, got %d", len(list))
	}
}

func TestTouchWorktreesSyncedAt(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	other := newRepo("/b", "b", "B")
	_ = c.CreateRepository(ctx, other)

	wt := &schema.Worktree{
		RepositoryID: repo.ID, BranchName: "feature/x",
		DirectoryPath: "/parent/a-x",
	}
	_ = c.CreateWorktree(ctx, wt)
	otherWT := &schema.Worktree{
		RepositoryID: other.ID, BranchName: "feature/y",
		DirectoryPath: "/parent/b-y",
	}
	_ = c.CreateWorktree(ctx, otherWT)

	// Never synced yet: last_synced_at reads as unset.
	list, _ := c.ListWorktrees(ctx, repo.ID)
	if list[0].LastSyncedAt != nil {
		t.Fatalf("expected nil LastSyncedAt before any sync, got %v", list[0].LastSyncedAt)
	}

	now := time.Now().Truncate(time.Second)
	if err := c.TouchWorktreesSyncedAt(ctx, repo.ID, now); err != nil {
		t.Fatalf("TouchWorktreesSyncedAt: %v", err)
	}

	list, _ = c.ListWorktrees(ctx, repo.ID)
	if list[0].LastSyncedAt == nil || !list[0].LastSyncedAt.Equal(now) {
		t.Errorf("LastSyncedAt = %v, want %v", list[0].LastSyncedAt, now)
	}

	// The other repository's worktree is untouched.
	otherList, _ := c.ListWorktrees(ctx, other.ID)
	if otherList[0].LastSyncedAt != nil {
		t.Errorf("other repo's worktree LastSyncedAt = %v, want nil", otherList[0].LastSyncedAt)
	}
}

func TestTrustSetupSteps(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	if err := c.CreateRepository(ctx, repo); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	got, err := c.TrustedSetupChecksums(ctx, repo.ID)
	if err != nil {
		t.Fatalf("TrustedSetupChecksums: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected nothing trusted yet, got %v", got)
	}

	for _, sum := range []string{"sha256:abc", "sha256:def", "sha256:abc"} {
		if err := c.TrustSetupSteps(ctx, repo.ID, sum); err != nil {
			t.Fatalf("TrustSetupSteps(%s): %v", sum, err)
		}
	}
	got, err = c.TrustedSetupChecksums(ctx, repo.ID)
	if err != nil {
		t.Fatalf("TrustedSetupChecksums: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("checksums = %v, want two (trusting twice must not duplicate)", got)
	}
	if !slices.Contains(got, "sha256:abc") || !slices.Contains(got, "sha256:def") {
		t.Errorf("checksums = %v, want both trusted values", got)
	}
}

func TestTrustedSetupChecksumsAreScopedToARepository(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo, other := newRepo("/a", "a", "A"), newRepo("/b", "b", "B")
	_ = c.CreateRepository(ctx, repo)
	_ = c.CreateRepository(ctx, other)
	if err := c.TrustSetupSteps(ctx, repo.ID, "sha256:abc"); err != nil {
		t.Fatalf("TrustSetupSteps: %v", err)
	}

	got, err := c.TrustedSetupChecksums(ctx, other.ID)
	if err != nil {
		t.Fatalf("TrustedSetupChecksums: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("other repo's checksums = %v, want none", got)
	}
}

func TestSetWorktreePR(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	wt := &schema.Worktree{
		RepositoryID: repo.ID, BranchName: "feature/x", DirectoryPath: "/parent/a-x",
	}
	if err := c.CreateWorktree(ctx, wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	num := int64(7)
	if err := c.SetWorktreePR(ctx, wt.ID, &num, "feature/x-renamed"); err != nil {
		t.Fatalf("SetWorktreePR: %v", err)
	}
	got, err := c.FindWorktree(ctx, repo.ID, "feature/x-renamed")
	if err != nil {
		t.Fatalf("FindWorktree: %v", err)
	}
	if got.GithubPRNumber == nil || *got.GithubPRNumber != num {
		t.Errorf("GithubPRNumber = %v, want %d", got.GithubPRNumber, num)
	}

	if err := c.SetWorktreePR(ctx, wt.ID, nil, "feature/x"); err != nil {
		t.Fatalf("SetWorktreePR(nil): %v", err)
	}
	got, err = c.FindWorktree(ctx, repo.ID, "feature/x")
	if err != nil {
		t.Fatalf("FindWorktree: %v", err)
	}
	if got.GithubPRNumber != nil {
		t.Errorf("GithubPRNumber = %v, want nil after unlinking", got.GithubPRNumber)
	}
}

func TestSetWorktreeDirectory(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	wt := &schema.Worktree{
		RepositoryID: repo.ID, BranchName: "feature/x", DirectoryPath: "/parent/a-x",
	}
	if err := c.CreateWorktree(ctx, wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if err := c.SetWorktreeDirectory(ctx, wt.ID, "/parent/a-feature-x"); err != nil {
		t.Fatalf("SetWorktreeDirectory: %v", err)
	}

	got, err := c.FindWorktree(ctx, repo.ID, "/parent/a-feature-x")
	if err != nil {
		t.Fatalf("FindWorktree at the new directory: %v", err)
	}
	if got.ID != wt.ID {
		t.Errorf("FindWorktree returned id %d, want %d", got.ID, wt.ID)
	}
	if _, err := c.FindWorktree(ctx, repo.ID, "/parent/a-x"); !errors.Is(err, ErrWorktreeNotFound) {
		t.Errorf("the old directory should no longer resolve, got %v", err)
	}
}

func TestSetWorktreeSetupError(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	wt := &schema.Worktree{
		RepositoryID: repo.ID, BranchName: "feature/x", DirectoryPath: "/parent/a-x",
	}
	if err := c.CreateWorktree(ctx, wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	list, err := c.ListWorktrees(ctx, repo.ID)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if list[0].SetupError != nil {
		t.Fatalf("expected no setup error initially, got %v", list[0].SetupError)
	}

	msg := "step 2 (run-command): exit status 1"
	if err := c.SetWorktreeSetupError(ctx, wt.ID, &msg); err != nil {
		t.Fatalf("SetWorktreeSetupError: %v", err)
	}
	list, _ = c.ListWorktrees(ctx, repo.ID)
	if list[0].SetupError == nil || *list[0].SetupError != msg {
		t.Errorf("SetupError = %v, want %q", list[0].SetupError, msg)
	}

	if err := c.SetWorktreeSetupError(ctx, wt.ID, nil); err != nil {
		t.Fatalf("SetWorktreeSetupError(nil): %v", err)
	}
	list, _ = c.ListWorktrees(ctx, repo.ID)
	if list[0].SetupError != nil {
		t.Errorf("SetupError = %v, want nil after clearing", list[0].SetupError)
	}
}

func TestDeleteRepository(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	// A second repo that must be left untouched.
	other := newRepo("/b", "b", "B")
	_ = c.CreateRepository(ctx, other)

	for _, dir := range []string{"/parent/a-x", "/parent/a-y"} {
		wt := &schema.Worktree{
			RepositoryID: repo.ID, BranchName: "feature/" + dir,
			DirectoryPath: dir,
		}
		if err := c.CreateWorktree(ctx, wt); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
	}
	otherWT := &schema.Worktree{
		RepositoryID: other.ID, BranchName: "feature/b",
		DirectoryPath: "/parent/b-x",
	}
	_ = c.CreateWorktree(ctx, otherWT)

	removed, err := c.DeleteRepository(ctx, repo.ID)
	if err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}
	if removed != 2 {
		t.Errorf("worktreesRemoved = %d, want 2", removed)
	}

	if _, err := c.repositoryByID(ctx, repo.ID); !errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("repository should be gone, got %v", err)
	}
	if wts, _ := c.ListWorktrees(ctx, repo.ID); len(wts) != 0 {
		t.Errorf("expected 0 worktrees after delete, got %d", len(wts))
	}

	// The other repository and its worktree survive.
	if _, err := c.repositoryByID(ctx, other.ID); err != nil {
		t.Errorf("other repository should survive, got %v", err)
	}
	if wts, _ := c.ListWorktrees(ctx, other.ID); len(wts) != 1 {
		t.Errorf("other repo should keep its worktree, got %d", len(wts))
	}
}

func TestDeleteRepositoryMissing(t *testing.T) {
	c := openTemp(t)
	if _, err := c.DeleteRepository(context.Background(), 999); !errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("expected ErrRepositoryNotFound, got %v", err)
	}
}

func openPRs(prs []schema.PullRequest) map[int64]schema.PullRequest {
	out := map[int64]schema.PullRequest{}
	for _, pr := range prs {
		if pr.DeletedAt == nil {
			out[pr.GithubPRNumber] = pr
		}
	}
	return out
}

func TestReplaceOpenPRs(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	now := time.Now()

	err := c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{
		{Number: 1, Branch: "feature/one", Author: "alice"},
		{Number: 2, Branch: "feature/two", Author: "bob"},
	}, now)
	if err != nil {
		t.Fatalf("ReplaceOpenPRs: %v", err)
	}
	prs, _ := c.listPRs(ctx, repo.ID)
	if len(openPRs(prs)) != 2 {
		t.Fatalf("expected 2 open PRs, got %+v", prs)
	}

	err = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{
		{Number: 2, Branch: "feature/two-renamed", Author: "bob"},
		{Number: 3, Branch: "feature/three", Author: "carol"},
	}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReplaceOpenPRs: %v", err)
	}
	prs, _ = c.listPRs(ctx, repo.ID)
	open := openPRs(prs)
	if len(open) != 2 || open[2].BranchName != "feature/two-renamed" || open[3].AuthorLogin != "carol" {
		t.Errorf("unexpected open PRs: %+v", open)
	}
	if len(prs) != 3 {
		t.Fatalf("closed PR #1 should be kept as a soft-deleted row, got %+v", prs)
	}
	if prs[0].GithubPRNumber != 1 || prs[0].DeletedAt == nil || !prs[0].DeletedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("PR #1 = %+v, want soft-deleted at the second sync", prs[0])
	}
	if prs[0].AuthorLogin != "alice" {
		t.Errorf("soft-deleted PR lost its author: %+v", prs[0])
	}
}

func TestReplaceOpenPRsEmptySoftDeletesAll(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	now := time.Now()

	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b"}}, now)
	if err := c.ReplaceOpenPRs(ctx, repo.ID, nil, now); err != nil {
		t.Fatalf("ReplaceOpenPRs(nil): %v", err)
	}
	prs, _ := c.listPRs(ctx, repo.ID)
	if len(prs) != 1 || len(openPRs(prs)) != 0 {
		t.Errorf("expected the PR kept but soft-deleted, got %+v", prs)
	}
}

func TestReplaceOpenPRsKeepsEarlierDeletionTime(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	now := time.Now()

	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b"}}, now)
	_ = c.ReplaceOpenPRs(ctx, repo.ID, nil, now.Add(time.Hour))
	_ = c.ReplaceOpenPRs(ctx, repo.ID, nil, now.Add(2*time.Hour))
	prs, _ := c.listPRs(ctx, repo.ID)
	if prs[0].DeletedAt == nil || !prs[0].DeletedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("deleted_at = %v, want the first sync that saw it closed", prs[0].DeletedAt)
	}
}

func TestReplaceOpenPRsRevivesReopenedPR(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	now := time.Now()

	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b", Author: "alice"}}, now)
	first, _ := c.listPRs(ctx, repo.ID)
	_ = c.ReplaceOpenPRs(ctx, repo.ID, nil, now.Add(time.Hour))
	if err := c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b", Author: "alice"}}, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("ReplaceOpenPRs: %v", err)
	}
	prs, _ := c.listPRs(ctx, repo.ID)
	if len(prs) != 1 || prs[0].ID != first[0].ID {
		t.Fatalf("reopened PR should reuse its row, got %+v (first %+v)", prs, first)
	}
	if prs[0].DeletedAt != nil {
		t.Errorf("reopened PR still soft-deleted: %+v", prs[0])
	}
}

func TestPullRequestsIncludesClosedPRs(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	other := newRepo("/b", "b", "B")
	_ = c.CreateRepository(ctx, repo)
	_ = c.CreateRepository(ctx, other)
	now := time.Now()

	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b", Author: "alice"}}, now)
	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 2, Branch: "c", Author: "bob"}}, now)
	_ = c.ReplaceOpenPRs(ctx, other.ID, []TrackedPR{{Number: 3, Branch: "d", Author: "carol"}}, now)

	got, err := c.PullRequests(ctx, repo.ID)
	if err != nil {
		t.Fatalf("PullRequests: %v", err)
	}
	if len(got) != 2 || got[1].AuthorLogin != "alice" || got[2].AuthorLogin != "bob" || got[1].DeletedAt == nil {
		t.Errorf("PullRequests = %v, want closed #1 by alice and open #2 by bob", got)
	}
}

func TestPullRequestsError(t *testing.T) {
	c := openTemp(t)
	_ = c.Close()
	if _, err := c.PullRequests(context.Background(), 1); err == nil {
		t.Error("expected an error from a closed database")
	}
}

func TestRecordClosedPR(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	now := time.Now()

	if err := c.RecordClosedPR(ctx, repo.ID, TrackedPR{Number: 5, Branch: "b", Author: "alice"}, now); err != nil {
		t.Fatalf("RecordClosedPR: %v", err)
	}
	prs, _ := c.listPRs(ctx, repo.ID)
	if len(prs) != 1 || prs[0].DeletedAt == nil || prs[0].AuthorLogin != "alice" {
		t.Fatalf("expected a soft-deleted row authored by alice, got %+v", prs)
	}

	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 6, Branch: "c"}}, now)
	if err := c.RecordClosedPR(ctx, repo.ID, TrackedPR{Number: 6, Branch: "c", Author: "bob"}, now); err != nil {
		t.Fatalf("RecordClosedPR on an existing row: %v", err)
	}
	prs, _ = c.listPRs(ctx, repo.ID)
	if prs[1].DeletedAt != nil || prs[1].AuthorLogin != "bob" {
		t.Errorf("an open row should keep its state and gain the author, got %+v", prs[1])
	}
}

func TestRecordClosedPRError(t *testing.T) {
	c := openTemp(t)
	_ = c.Close()
	if err := c.RecordClosedPR(context.Background(), 1, TrackedPR{Number: 1}, time.Now()); err == nil {
		t.Error("expected an error from a closed database")
	}
}

func TestSyncRuns(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)

	if latest, err := c.LatestSyncRun(ctx, repo.ID); err != nil || latest != nil {
		t.Fatalf("LatestSyncRun (none) = %v, %v", latest, err)
	}

	start := time.Now()
	run, err := c.StartSyncRun(ctx, repo.ID, start)
	if err != nil {
		t.Fatalf("StartSyncRun: %v", err)
	}
	if err := c.FinishSyncRun(ctx, run, start.Add(time.Second), 3, 1, nil); err != nil {
		t.Fatalf("FinishSyncRun: %v", err)
	}

	latest, err := c.LatestSyncRun(ctx, repo.ID)
	if err != nil {
		t.Fatalf("LatestSyncRun: %v", err)
	}
	if latest == nil || latest.WorktreesCreated != 3 || latest.WorktreesRemoved != 1 {
		t.Errorf("latest = %+v", latest)
	}
	if latest.Status != schema.SyncStatusOK || latest.FinishedAt == nil {
		t.Errorf("latest status/finish = %v, %v", latest.Status, latest.FinishedAt)
	}
}

func TestFinishSyncRunError(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	run, _ := c.StartSyncRun(ctx, repo.ID, time.Now())
	if err := c.FinishSyncRun(ctx, run, time.Now(), 0, 0, errors.New("kaboom")); err != nil {
		t.Fatalf("FinishSyncRun: %v", err)
	}
	latest, _ := c.LatestSyncRun(ctx, repo.ID)
	if latest.Status != schema.SyncStatusError || latest.Error == nil || *latest.Error != "kaboom" {
		t.Errorf("latest = %+v", latest)
	}
}

func TestCreateWorktreeOrigin(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)

	adopted := &schema.Worktree{RepositoryID: repo.ID, BranchName: "x", DirectoryPath: "/a-x"}
	local := &schema.Worktree{RepositoryID: repo.ID, BranchName: "y", DirectoryPath: "/a-y", Origin: schema.OriginLocal}
	for _, wt := range []*schema.Worktree{adopted, local} {
		if err := c.CreateWorktree(ctx, wt); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
	}
	wts, _ := c.ListWorktrees(ctx, repo.ID)
	if wts[0].Origin != schema.OriginAdopted || wts[1].Origin != schema.OriginLocal {
		t.Errorf("origins = %q, %q; want adopted, local", wts[0].Origin, wts[1].Origin)
	}
}

func TestCreateWorktreeRejectsUnknownOrigin(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)

	wt := &schema.Worktree{RepositoryID: repo.ID, BranchName: "x", DirectoryPath: "/a-x", Origin: "elsewhere"}
	if err := c.CreateWorktree(ctx, wt); err == nil {
		t.Error("expected the origin CHECK constraint to reject an unknown value")
	}
}

func TestSetWorktreeAuthorshipOverride(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	wt := &schema.Worktree{RepositoryID: repo.ID, BranchName: "x", DirectoryPath: "/a-x"}
	_ = c.CreateWorktree(ctx, wt)

	mine := schema.AuthorshipMine
	if err := c.SetWorktreeAuthorshipOverride(ctx, wt.ID, &mine); err != nil {
		t.Fatalf("SetWorktreeAuthorshipOverride: %v", err)
	}
	wts, _ := c.ListWorktrees(ctx, repo.ID)
	if wts[0].AuthorshipOverride == nil || *wts[0].AuthorshipOverride != schema.AuthorshipMine {
		t.Fatalf("override = %v, want mine", wts[0].AuthorshipOverride)
	}

	if err := c.SetWorktreeAuthorshipOverride(ctx, wt.ID, nil); err != nil {
		t.Fatalf("clearing override: %v", err)
	}
	wts, _ = c.ListWorktrees(ctx, repo.ID)
	if wts[0].AuthorshipOverride != nil {
		t.Errorf("override = %v, want cleared", *wts[0].AuthorshipOverride)
	}

	bogus := schema.Authorship("ours")
	if err := c.SetWorktreeAuthorshipOverride(ctx, wt.ID, &bogus); err == nil {
		t.Error("expected the authorship CHECK constraint to reject an unknown value")
	}
}

func TestFirstSeenAtIsSetOnceAndSurvivesReopening(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	now := time.Now().UTC()

	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b"}}, now)
	_ = c.ReplaceOpenPRs(ctx, repo.ID, nil, now.Add(time.Hour))
	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b"}}, now.Add(2*time.Hour))
	_ = c.RecordClosedPR(ctx, repo.ID, TrackedPR{Number: 2, Branch: "c"}, now.Add(3*time.Hour))

	prs, _ := c.PullRequests(ctx, repo.ID)
	if prs[1].FirstSeenAt == nil || !prs[1].FirstSeenAt.Equal(now) {
		t.Errorf("#1 first_seen_at = %v, want the first sync", prs[1].FirstSeenAt)
	}
	if prs[2].FirstSeenAt == nil || !prs[2].FirstSeenAt.Equal(now.Add(3*time.Hour)) {
		t.Errorf("#2 first_seen_at = %v, want when it was recorded", prs[2].FirstSeenAt)
	}
}

func TestUpdatePRConditions(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	t0 := time.Now().UTC()
	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b"}}, t0)
	read := func() schema.PullRequest {
		prs, _ := c.PullRequests(ctx, repo.ID)
		return prs[1]
	}
	same := func(got *time.Time, want time.Time) bool { return got != nil && got.Equal(want) }

	steps := []struct {
		at                  time.Time
		cond                PRCondition
		conflicted, failing *time.Time
	}{
		{t0.Add(1 * time.Hour), PRCondition{Number: 1, Conflicted: true, ChecksFailing: true}, ptrTime(t0.Add(time.Hour)), ptrTime(t0.Add(time.Hour))},
		{t0.Add(2 * time.Hour), PRCondition{Number: 1, Conflicted: true, ChecksFailing: true}, ptrTime(t0.Add(time.Hour)), ptrTime(t0.Add(time.Hour))},
		{t0.Add(3 * time.Hour), PRCondition{Number: 1, ConflictUnknown: true}, ptrTime(t0.Add(time.Hour)), nil},
		{t0.Add(4 * time.Hour), PRCondition{Number: 1}, nil, nil},
		{t0.Add(5 * time.Hour), PRCondition{Number: 1, Conflicted: true}, ptrTime(t0.Add(5 * time.Hour)), nil},
	}
	for i, st := range steps {
		if err := c.UpdatePRConditions(ctx, repo.ID, []PRCondition{st.cond}, st.at); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		pr := read()
		if (st.conflicted == nil) != (pr.ConflictedSince == nil) || (st.conflicted != nil && !same(pr.ConflictedSince, *st.conflicted)) {
			t.Errorf("step %d: conflicted_since = %v, want %v", i, pr.ConflictedSince, st.conflicted)
		}
		if (st.failing == nil) != (pr.ChecksFailingSince == nil) || (st.failing != nil && !same(pr.ChecksFailingSince, *st.failing)) {
			t.Errorf("step %d: checks_failing_since = %v, want %v", i, pr.ChecksFailingSince, st.failing)
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestUpdatePRConditionsError(t *testing.T) {
	c := openTemp(t)
	_ = c.Close()
	if err := c.UpdatePRConditions(context.Background(), 1, []PRCondition{{Number: 1}}, time.Now()); err == nil {
		t.Error("expected an error from a closed database")
	}
}

func TestMarkViewed(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	t0 := time.Now().UTC()

	views, err := c.ListViews(ctx, repo.ID)
	if err != nil || len(views) != 0 {
		t.Fatalf("views = %v, %v; want none before anything is viewed", views, err)
	}
	for _, step := range []struct {
		a  schema.Authorship
		at time.Time
	}{
		{schema.AuthorshipMine, t0},
		{schema.AuthorshipMine, t0.Add(time.Hour)},
		{schema.AuthorshipMine, t0.Add(30 * time.Minute)},
		{schema.AuthorshipTheirs, t0},
	} {
		if err := c.MarkViewed(ctx, repo.ID, step.a, step.at); err != nil {
			t.Fatalf("MarkViewed: %v", err)
		}
	}
	views, _ = c.ListViews(ctx, repo.ID)
	if !views[schema.AuthorshipMine].Equal(t0.Add(time.Hour)) || !views[schema.AuthorshipTheirs].Equal(t0) {
		t.Errorf("views = %v, want mine at the latest view (never moved back) and theirs at its own", views)
	}

	if err := c.MarkViewed(ctx, repo.ID, "unknown", t0); err == nil {
		t.Error("expected the authorship CHECK constraint to reject an unknown tab")
	}
}

func TestListViewsErrors(t *testing.T) {
	c := openTemp(t)
	_ = c.Close()
	if _, err := c.ListViews(context.Background(), 1); err == nil {
		t.Error("expected ListViews to fail on a closed database")
	}
	if err := c.MarkViewed(context.Background(), 1, schema.AuthorshipMine, time.Now()); err == nil {
		t.Error("expected MarkViewed to fail on a closed database")
	}
}

func TestUpdatePRConditionsUpdateFailure(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	_ = c.CreateRepository(ctx, repo)
	_ = c.ReplaceOpenPRs(ctx, repo.ID, []TrackedPR{{Number: 1, Branch: "b"}}, time.Now())
	if _, err := c.ExecContext(ctx, "CREATE TRIGGER no_update BEFORE UPDATE ON pull_requests BEGIN SELECT RAISE(ABORT, 'boom'); END"); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdatePRConditions(ctx, repo.ID, []PRCondition{{Number: 1}}, time.Now()); err == nil {
		t.Error("expected the update failure to be reported")
	}
}

func TestMarkViewedReadFailure(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	if _, err := c.ExecContext(ctx, "ALTER TABLE list_views RENAME COLUMN viewed_at TO gone"); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkViewed(ctx, 1, schema.AuthorshipMine, time.Now()); err == nil {
		t.Error("expected the read failure to be reported")
	}
}
