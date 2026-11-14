package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func authorshipByBranch(t *testing.T, h *harness, repo *schema.Repository) map[string]schema.Authorship {
	t.Helper()
	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	out := map[string]schema.Authorship{}
	for _, v := range views {
		out[v.Worktree.BranchName] = v.Authorship
	}
	return out
}

func TestWorktreeViewsResolveAuthorship(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "me"
	h.gh.logins = []string{"Me-Work"}
	h.gh.prs = []github.PR{
		{Number: 1, HeadBranch: "mine-personal", AuthorLogin: "me"},
		{Number: 2, HeadBranch: "mine-work", AuthorLogin: "me-work"},
		{Number: 3, HeadBranch: "theirs", AuthorLogin: "dmitri"},
	}
	h.seedSync(t, repo)
	if _, err := h.svc.AddWorktree(context.Background(), repo, "local-only"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	got := authorshipByBranch(t, h, repo)
	want := map[string]schema.Authorship{
		"mine-personal": schema.AuthorshipMine,
		"mine-work":     schema.AuthorshipMine,
		"theirs":        schema.AuthorshipTheirs,
		"local-only":    schema.AuthorshipMine,
	}
	for branch, a := range want {
		if got[branch] != a {
			t.Errorf("%s = %q, want %q", branch, got[branch], a)
		}
	}
}

func TestWorktreeViewsUseRepositoryLoginBeforeFirstSync(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "me"
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a", AuthorLogin: "me"}}
	h.seedSync(t, repo)
	h.svc.identities.Delete(repo.ID)
	h.gh.loginsErr = errors.New("gh must not be asked on a read")

	if got := authorshipByBranch(t, h, repo); got["a"] != schema.AuthorshipMine {
		t.Errorf("authorship = %q, want mine from the repository's own login", got["a"])
	}
}

func TestSyncKeepsIdentitiesWhenListingLoginsFails(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.logins = []string{"work"}
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a", AuthorLogin: "work"}}
	h.seedSync(t, repo)

	h.gh.loginsErr = errors.New("gh auth status failed")
	h.seedSync(t, repo)

	if got := authorshipByBranch(t, h, repo); got["a"] != schema.AuthorshipMine {
		t.Errorf("authorship = %q, want mine from the identities of the earlier sync", got["a"])
	}
}

func TestOrphanedWorktreeKeepsItsAuthor(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "me"
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a", AuthorLogin: "me"}}
	h.seedSync(t, repo)
	wt := h.syncedWorktree(t, repo)
	h.git.dirty[wt.DirectoryPath] = true
	h.gh.prs = nil
	h.seedSync(t, repo)

	if got := authorshipByBranch(t, h, repo); got["a"] != schema.AuthorshipMine {
		t.Errorf("authorship = %q, want mine from the closed PR's author", got["a"])
	}
}

func TestAdoptedWorktreeWithoutPRIsTheirs(t *testing.T) {
	h := newHarness(t)
	repo := h.unlinkedWorktree(t, "feature/x")
	if got := authorshipByBranch(t, h, repo); got["feature/x"] != schema.AuthorshipTheirs {
		t.Errorf("authorship = %q, want theirs", got["feature/x"])
	}
}

func TestSetLoginRefreshesIdentities(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a", AuthorLogin: "octocat"}}
	h.seedSync(t, repo)
	if got := authorshipByBranch(t, h, repo); got["a"] != schema.AuthorshipTheirs {
		t.Fatalf("authorship = %q before set-login, want theirs", got["a"])
	}

	h.gh.logins = []string{"octocat"}
	if _, err := h.svc.SetLogin(context.Background(), repo, "octocat"); err != nil {
		t.Fatalf("SetLogin: %v", err)
	}
	if got := authorshipByBranch(t, h, repo); got["a"] != schema.AuthorshipMine {
		t.Errorf("authorship = %q after set-login, want mine", got["a"])
	}
}

func TestSetAuthorship(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a", AuthorLogin: "dmitri"}}
	h.seedSync(t, repo)
	wt := h.syncedWorktree(t, repo)

	mine := schema.AuthorshipMine
	view, err := h.svc.SetAuthorship(context.Background(), repo, "feature/a", &mine)
	if err != nil {
		t.Fatalf("SetAuthorship: %v", err)
	}
	if view.Authorship != schema.AuthorshipMine || view.Worktree.AuthorshipOverride == nil {
		t.Errorf("view = %+v, want an overridden mine", view)
	}

	nested := filepath.Join(wt.DirectoryPath, "pkg", "deep")
	view, err = h.svc.SetAuthorship(context.Background(), repo, nested, nil)
	if err != nil {
		t.Fatalf("SetAuthorship from a subdirectory: %v", err)
	}
	if view.Authorship != schema.AuthorshipTheirs || view.Worktree.AuthorshipOverride != nil {
		t.Errorf("view = %+v, want the override cleared and theirs derived again", view)
	}
}

func TestSetAuthorshipPrefersTheDeepestEnclosingWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	outer := filepath.Join(h.parent, "outer")
	inner := filepath.Join(outer, "inner")
	for _, wt := range []*schema.Worktree{
		{RepositoryID: repo.ID, BranchName: "outer", DirectoryPath: outer},
		{RepositoryID: repo.ID, BranchName: "inner", DirectoryPath: inner},
	} {
		if err := h.db.CreateWorktree(context.Background(), wt); err != nil {
			t.Fatal(err)
		}
	}

	theirs := schema.AuthorshipTheirs
	view, err := h.svc.SetAuthorship(context.Background(), repo, filepath.Join(inner, "x"), &theirs)
	if err != nil {
		t.Fatalf("SetAuthorship: %v", err)
	}
	if view.Worktree.BranchName != "inner" {
		t.Errorf("set %q, want the deepest enclosing worktree", view.Worktree.BranchName)
	}
}

func TestSetAuthorshipNotFound(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)

	for _, ref := range []string{"nope", filepath.Join(h.parent, "elsewhere"), filepath.Join(h.parent, "n-a-sibling")} {
		if _, err := h.svc.SetAuthorship(context.Background(), repo, ref, nil); !errors.Is(err, database.ErrWorktreeNotFound) {
			t.Errorf("%s: err = %v, want ErrWorktreeNotFound", ref, err)
		}
	}
}

func TestSetAuthorshipReportsViewErrors(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	h.gh.prsErr = errors.New("gh down")

	if _, err := h.svc.SetAuthorship(context.Background(), repo, "feature/a", nil); err == nil {
		t.Error("expected the view failure to be reported")
	}
}

func TestSetAuthorshipReportsDatabaseErrors(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	if _, err := h.db.ExecContext(context.Background(),
		"CREATE TRIGGER fail_override BEFORE UPDATE OF authorship_override ON worktrees BEGIN SELECT RAISE(ABORT, 'boom'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetAuthorship(context.Background(), repo, "feature/a", nil); err == nil {
		t.Error("expected the update failure to be reported")
	}

	_ = h.db.Close()
	if _, err := h.svc.SetAuthorship(context.Background(), repo, "feature/a", nil); err == nil {
		t.Error("expected the listing failure to be reported")
	}
}

func TestServerSetAuthorship(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a", AuthorLogin: "dmitri"}}
	h.seedSync(t, repo)

	mine := lumberjackv1.Authorship_AUTHORSHIP_MINE
	resp, err := srv.SetAuthorship(context.Background(), &lumberjackv1.SetAuthorshipRequest{
		Repository: "n", Worktree: "feature/a", Authorship: &mine,
	})
	if err != nil {
		t.Fatalf("SetAuthorship: %v", err)
	}
	if wt := resp.GetWorktree(); wt.GetAuthorship() != mine || !wt.GetAuthorshipOverridden() {
		t.Errorf("worktree = %+v", wt)
	}

	resp, err = srv.SetAuthorship(context.Background(), &lumberjackv1.SetAuthorshipRequest{Repository: "n", Worktree: "feature/a"})
	if err != nil {
		t.Fatalf("SetAuthorship auto: %v", err)
	}
	if wt := resp.GetWorktree(); wt.GetAuthorship() != lumberjackv1.Authorship_AUTHORSHIP_THEIRS || wt.GetAuthorshipOverridden() {
		t.Errorf("worktree = %+v, want derived theirs", wt)
	}
}

func TestServerSetAuthorshipFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	unspecified := lumberjackv1.Authorship_AUTHORSHIP_UNSPECIFIED

	cases := map[string]struct {
		req  *lumberjackv1.SetAuthorshipRequest
		want codes.Code
	}{
		"no worktree":        {&lumberjackv1.SetAuthorshipRequest{Repository: "n"}, codes.InvalidArgument},
		"unspecified":        {&lumberjackv1.SetAuthorshipRequest{Repository: "n", Worktree: "feature/a", Authorship: &unspecified}, codes.InvalidArgument},
		"unknown repository": {&lumberjackv1.SetAuthorshipRequest{Repository: "nope", Worktree: "feature/a"}, codes.NotFound},
		"unknown worktree":   {&lumberjackv1.SetAuthorshipRequest{Repository: "n", Worktree: "nope"}, codes.NotFound},
	}
	for name, tc := range cases {
		_, err := srv.SetAuthorship(context.Background(), tc.req)
		if status.Code(err) != tc.want {
			t.Errorf("%s: code = %v, want %v (%v)", name, status.Code(err), tc.want, err)
		}
	}
}

func TestToProtoAuthorship(t *testing.T) {
	cases := map[schema.Authorship]lumberjackv1.Authorship{
		schema.AuthorshipMine:   lumberjackv1.Authorship_AUTHORSHIP_MINE,
		schema.AuthorshipTheirs: lumberjackv1.Authorship_AUTHORSHIP_THEIRS,
		"":                      lumberjackv1.Authorship_AUTHORSHIP_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := toProtoAuthorship(in); got != want {
			t.Errorf("toProtoAuthorship(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWorktreeViewsReportsAuthorLookupFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	if _, err := h.db.ExecContext(context.Background(), "ALTER TABLE pull_requests RENAME COLUMN author_login TO gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.WorktreeViews(context.Background(), repo); err == nil {
		t.Error("expected the author lookup failure to be reported")
	}
}

func TestAdoptedBranchAuthorshipFromCommits(t *testing.T) {
	cases := map[string]struct {
		sha     string
		authors github.CommitAuthors
		want    schema.Authorship
	}{
		"never pushed":         {"", github.CommitAuthors{}, schema.AuthorshipMine},
		"every commit by me":   {"abc", github.CommitAuthors{Logins: []string{"me", "me"}}, schema.AuthorshipMine},
		"a commit by someone":  {"abc", github.CommitAuthors{Logins: []string{"me", "dmitri"}}, schema.AuthorshipTheirs},
		"nothing of its own":   {"abc", github.CommitAuthors{}, schema.AuthorshipTheirs},
		"too many to classify": {"abc", github.CommitAuthors{Logins: []string{"me"}, Truncated: true}, schema.AuthorshipTheirs},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			repo := h.unlinkedWorktree(t, "feature/x")
			repo.Login = "me"
			h.git.remoteSHAs = map[string]string{"feature/x": tc.sha}
			h.gh.compare = map[string]github.CommitAuthors{"abc": tc.authors}
			h.seedSync(t, repo)

			if got := authorshipByBranch(t, h, repo); got["feature/x"] != tc.want {
				t.Errorf("authorship = %q, want %q", got["feature/x"], tc.want)
			}
		})
	}
}

func TestCommitAuthorsAreCachedOnTheHeadSHA(t *testing.T) {
	h := newHarness(t)
	repo := h.unlinkedWorktree(t, "feature/x")
	h.git.remoteSHAs = map[string]string{"feature/x": "abc"}
	h.gh.compare = map[string]github.CommitAuthors{"abc": {Logins: []string{"me"}}}
	h.seedSync(t, repo)
	h.seedSync(t, repo)
	if len(h.gh.compareCalls) != 1 || h.gh.compareCalls[0] != "main...abc" {
		t.Errorf("compare calls = %v, want one against the default branch", h.gh.compareCalls)
	}

	h.git.remoteSHAs["feature/x"] = "def"
	h.seedSync(t, repo)
	if len(h.gh.compareCalls) != 2 {
		t.Errorf("compare calls = %v, want a new lookup once the head moves", h.gh.compareCalls)
	}
}

func TestCommitAuthorsAreNotFetchedWhenOtherwiseDecided(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "with-pr"}}
	h.seedSync(t, repo)
	if _, err := h.svc.AddWorktree(context.Background(), repo, "local"); err != nil {
		t.Fatal(err)
	}
	h.git.remoteSHAs = map[string]string{"with-pr": "abc", "local": "def"}
	h.seedSync(t, repo)
	if len(h.gh.compareCalls) != 0 {
		t.Errorf("compare calls = %v, want none for a PR or a locally created worktree", h.gh.compareCalls)
	}
}

func TestCommitAuthorFailuresLeaveTheBranchTheirs(t *testing.T) {
	cases := map[string]func(h *harness){
		"remote lookup": func(h *harness) { h.git.remoteSHAErr = errors.New("git broke") },
		"default branch": func(h *harness) {
			h.git.remoteSHAs = map[string]string{"feature/x": "abc"}
			h.git.defaultBranchErr = errors.New("no HEAD")
		},
		"compare": func(h *harness) {
			h.git.remoteSHAs = map[string]string{"feature/x": "abc"}
			h.gh.compareErr = errors.New("HTTP 500")
		},
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			repo := h.unlinkedWorktree(t, "feature/x")
			repo.Login = "me"
			h.gh.compare = map[string]github.CommitAuthors{"abc": {Logins: []string{"me"}}}
			fail(h)
			h.seedSync(t, repo)
			if got := authorshipByBranch(t, h, repo); got["feature/x"] != schema.AuthorshipTheirs {
				t.Errorf("authorship = %q, want theirs", got["feature/x"])
			}
		})
	}
}
