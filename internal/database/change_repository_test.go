package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func TestChangeGithubRepository(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	repo := newRepo("/a", "a", "A")
	other := newRepo("/b", "b", "B")
	for _, r := range []*schema.Repository{repo, other} {
		if err := c.CreateRepository(ctx, r); err != nil {
			t.Fatalf("CreateRepository: %v", err)
		}
	}
	now := time.Now()
	for _, r := range []*schema.Repository{repo, other} {
		if err := c.ReplaceOpenPRs(ctx, r.ID, []TrackedPR{{Number: 4, Branch: "feature/x"}}, now); err != nil {
			t.Fatalf("ReplaceOpenPRs: %v", err)
		}
		if err := c.MarkViewed(ctx, r.ID, schema.AuthorshipMine, now); err != nil {
			t.Fatalf("MarkViewed: %v", err)
		}
	}
	pr := int64(4)
	wt := &schema.Worktree{RepositoryID: repo.ID, GithubPRNumber: &pr, BranchName: "feature/x", DirectoryPath: "/parent/a-x"}
	otherWT := &schema.Worktree{RepositoryID: other.ID, GithubPRNumber: &pr, BranchName: "feature/x", DirectoryPath: "/parent/b-x"}
	for _, w := range []*schema.Worktree{wt, otherWT} {
		if err := c.CreateWorktree(ctx, w); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
	}

	if err := c.ChangeGithubRepository(ctx, repo.ID, "fork", "A2"); err != nil {
		t.Fatalf("ChangeGithubRepository: %v", err)
	}

	got, _ := c.repositoryByID(ctx, repo.ID)
	if got.GithubOwner != "fork" || got.GithubName != "A2" {
		t.Errorf("repository = %s/%s, want fork/A2", got.GithubOwner, got.GithubName)
	}
	if prs, _ := c.PullRequests(ctx, repo.ID); len(prs) != 0 {
		t.Errorf("pull requests = %v, want none", prs)
	}
	if views, _ := c.ListViews(ctx, repo.ID); len(views) != 0 {
		t.Errorf("list views = %v, want none", views)
	}
	wts, _ := c.ListWorktrees(ctx, repo.ID)
	if len(wts) != 1 || wts[0].GithubPRNumber != nil || wts[0].BranchName != "feature/x" {
		t.Errorf("worktrees = %+v, want one unlinked feature/x", wts)
	}

	if prs, _ := c.PullRequests(ctx, other.ID); len(prs) != 1 {
		t.Errorf("other repository pull requests = %v, want untouched", prs)
	}
	if views, _ := c.ListViews(ctx, other.ID); len(views) != 1 {
		t.Errorf("other repository list views = %v, want untouched", views)
	}
	if wts, _ := c.ListWorktrees(ctx, other.ID); len(wts) != 1 || wts[0].GithubPRNumber == nil {
		t.Errorf("other repository worktrees = %+v, want still linked", wts)
	}
}

func TestChangeGithubRepositoryNotFound(t *testing.T) {
	c := openTemp(t)
	if err := c.ChangeGithubRepository(context.Background(), 99, "o", "n"); !errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("expected ErrRepositoryNotFound, got %v", err)
	}
}
