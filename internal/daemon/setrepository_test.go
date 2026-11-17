package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/github"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSetRepositoryResetsPRsAndKeepsWorktrees(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	before := h.syncedWorktree(t, repo)
	h.svc.noPRBranches.Store(fmt.Sprintf("%d\x00feature/y", repo.ID), h.svc.now())

	updated, err := h.svc.SetRepository(context.Background(), repo, "fork", "other")
	if err != nil {
		t.Fatalf("SetRepository: %v", err)
	}
	if updated.GithubOwner != "fork" || updated.GithubName != "other" {
		t.Errorf("repository = %s/%s, want fork/other", updated.GithubOwner, updated.GithubName)
	}
	if prs, _ := h.db.PullRequests(context.Background(), repo.ID); len(prs) != 0 {
		t.Errorf("pull requests = %v, want none", prs)
	}
	wt := h.syncedWorktree(t, repo)
	if wt.ID != before.ID || wt.GithubPRNumber != nil {
		t.Errorf("worktree = %+v, want the same row unlinked", wt)
	}
	if _, ok := h.svc.rollups.Load(repo.ID); ok {
		t.Error("rollup cache kept after set-repository")
	}
	if _, ok := h.svc.noPRBranches.Load(fmt.Sprintf("%d\x00feature/y", repo.ID)); ok {
		t.Error("no-PR cache kept after set-repository")
	}

	h.gh.prs = []github.PR{{Number: 9, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	wt = h.syncedWorktree(t, repo)
	if wt.ID != before.ID || wt.GithubPRNumber == nil || *wt.GithubPRNumber != 9 {
		t.Errorf("worktree after sync = %+v, want the same row linked to #9", wt)
	}
}

func TestSetRepositorySameRepositoryIsNoop(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)

	if _, err := h.svc.SetRepository(context.Background(), repo, "O", "N"); err != nil {
		t.Fatalf("SetRepository: %v", err)
	}
	if prs, _ := h.db.PullRequests(context.Background(), repo.ID); len(prs) != 1 {
		t.Errorf("pull requests = %v, want untouched", prs)
	}
	if len(h.gh.accessChecks) != 0 {
		t.Errorf("access checked %d times for an unchanged repository", len(h.gh.accessChecks))
	}
}

func TestSetRepositoryRejectsInaccessible(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	h.gh.accessErr = fmt.Errorf("HTTP 404: Not Found")

	if _, err := h.svc.SetRepository(context.Background(), repo, "fork", "other"); err == nil {
		t.Fatal("expected an error for an inaccessible repository")
	}
	if repo.GithubOwner != "o" || repo.GithubName != "n" {
		t.Errorf("repository = %s/%s, want unchanged", repo.GithubOwner, repo.GithubName)
	}
	if prs, _ := h.db.PullRequests(context.Background(), repo.ID); len(prs) != 1 {
		t.Errorf("pull requests = %v, want untouched", prs)
	}
}

func TestSetRepositoryRequiresOwnerAndName(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if _, err := h.svc.SetRepository(context.Background(), repo, "", "n"); err == nil {
		t.Error("expected an error for an empty owner")
	}
}

func TestServerSetRepository(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	resp, err := srv.SetRepository(context.Background(),
		&lumberjackv1.SetRepositoryRequest{Repository: "n", GithubOwner: "fork", GithubName: "other"})
	if err != nil {
		t.Fatalf("SetRepository: %v", err)
	}
	if r := resp.GetRepository(); r.GetGithubOwner() != "fork" || r.GetGithubName() != "other" {
		t.Errorf("repository = %s/%s, want fork/other", r.GetGithubOwner(), r.GetGithubName())
	}
}

func TestServerSetRepositoryFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	cases := map[string]struct {
		req    *lumberjackv1.SetRepositoryRequest
		access error
		want   codes.Code
	}{
		"no repository": {req: &lumberjackv1.SetRepositoryRequest{GithubOwner: "o", GithubName: "x"}, want: codes.InvalidArgument},
		"no owner":      {req: &lumberjackv1.SetRepositoryRequest{Repository: "n", GithubName: "x"}, want: codes.InvalidArgument},
		"no name":       {req: &lumberjackv1.SetRepositoryRequest{Repository: "n", GithubOwner: "o"}, want: codes.InvalidArgument},
		"unknown repository": {
			req: &lumberjackv1.SetRepositoryRequest{Repository: "nope", GithubOwner: "o", GithubName: "x"}, want: codes.NotFound,
		},
		"inaccessible": {
			req:    &lumberjackv1.SetRepositoryRequest{Repository: "n", GithubOwner: "o", GithubName: "x"},
			access: fmt.Errorf("HTTP 404: Not Found"), want: codes.Internal,
		},
	}
	for name, c := range cases {
		h.gh.accessErr = c.access
		if _, err := srv.SetRepository(context.Background(), c.req); status.Code(err) != c.want {
			t.Errorf("%s: expected %v, got %v", name, c.want, err)
		}
	}
}
