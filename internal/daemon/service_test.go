package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/worktree"
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
	remoteURL string
	urlErr    error
	// worktrees is what ListWorktrees reports — the worktrees git already has
	// registered (used to exercise adoption of hand-checked-out directories).
	// listErr forces the listing to fail.
	worktrees []worktree.Ref
	listErr   error
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

func (f *fakeGit) RemoteURL(context.Context, string, string) (string, error) {
	return f.remoteURL, f.urlErr
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

func (f *fakeGit) ListWorktrees(context.Context, string) ([]worktree.Ref, error) {
	return f.worktrees, f.listErr
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
	user    string
	userErr error
	// active is the account gh reports as currently signed in; switchErr forces
	// SwitchAccount to fail. switches records each (host, login) switch made.
	active    string
	activeErr error
	switchErr error
	switches  [][2]string
	// logins is what ListLogins reports for any host; loginsErr forces it to
	// fail. A nil logins slice means gh has no accounts.
	logins    []string
	loginsErr error
	// accessErr, if set, is returned by CheckRepoAccess — simulating a login gh
	// knows but that cannot reach the repo. accessChecks records each account
	// active when CheckRepoAccess ran.
	accessErr    error
	accessChecks []string
}

func (f *fakeGH) RepoInfo(context.Context, string) (github.RepoInfo, error) {
	return f.info, f.infoErr
}

func (f *fakeGH) ListOpenPRs(context.Context, github.RepoInfo) ([]github.PR, error) {
	return f.prs, f.prsErr
}

func (f *fakeGH) AuthenticatedUser(context.Context) (string, error) {
	return f.user, f.userErr
}

func (f *fakeGH) ActiveLogin(context.Context, string) (string, error) {
	return f.active, f.activeErr
}

func (f *fakeGH) SwitchAccount(_ context.Context, host, login string) error {
	if f.switchErr != nil {
		return f.switchErr
	}
	f.switches = append(f.switches, [2]string{host, login})
	f.active = login
	return nil
}

func (f *fakeGH) ListLogins(context.Context, string) ([]string, error) {
	return f.logins, f.loginsErr
}

func (f *fakeGH) CheckRepoAccess(context.Context, github.RepoInfo) error {
	// Record the account active at check time so tests can assert the check ran
	// under the candidate login rather than whatever was active before.
	f.accessChecks = append(f.accessChecks, f.active)
	return f.accessErr
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

	repo, _, err := h.svc.InitRepository(context.Background(), dir)
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

func TestInitRepositoryAdoptsExistingWorktrees(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.parent, "myrepo")
	existing := filepath.Join(h.parent, "myrepo-feature")
	// git reports the main checkout plus a hand-created worktree; only the latter
	// is adoptable (the main checkout and detached HEADs are skipped).
	h.git.worktrees = []worktree.Ref{
		{Dir: dir, Branch: "main"},
		{Dir: existing, Branch: "feature/x"},
		{Dir: filepath.Join(h.parent, "detached"), Branch: ""},
	}

	repo, adopted, err := h.svc.InitRepository(context.Background(), dir)
	if err != nil {
		t.Fatalf("InitRepository: %v", err)
	}
	if len(adopted) != 1 || adopted[0] != "feature/x" {
		t.Errorf("adopted=%v, want [feature/x]", adopted)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	if wts[0].DirectoryPath != existing || wts[0].BranchName != "feature/x" {
		t.Errorf("adopted wrong worktree: %+v", wts[0])
	}
	if wts[0].CreatedBy != schema.CreatedByPreexisting {
		t.Errorf("created_by=%q, want %q", wts[0].CreatedBy, schema.CreatedByPreexisting)
	}
	if wts[0].GithubPRNumber != nil {
		t.Errorf("adopted worktree should have no PR number yet, got %v", *wts[0].GithubPRNumber)
	}
}

func TestSyncLinksAdoptedWorktreeToPR(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.parent, "n")
	existing := filepath.Join(h.parent, "n-feature")
	h.git.worktrees = []worktree.Ref{
		{Dir: dir, Branch: "main"},
		{Dir: existing, Branch: "feature/x"},
	}
	// Init adopts the hand-created worktree with no PR number.
	repo, adopted, err := h.svc.InitRepository(context.Background(), dir)
	if err != nil || len(adopted) != 1 {
		t.Fatalf("init: adopted=%v err=%v", adopted, err)
	}

	// A sync then sees an open PR on that branch: it must link the existing row,
	// not try to recreate the branch (which would fail — it is already checked
	// out). AddWorktree failing proves creation was never attempted.
	h.gh.prs = []github.PR{{Number: 7, HeadBranch: "feature/x"}}
	h.git.addErr["feature/x"] = errors.New("a branch named 'feature/x' already exists")

	var msgs []string
	created, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(m string) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d, want 0 (linked, not created)", created)
	}
	if !containsSubstr(msgs, "feature/x: updated") {
		t.Errorf("expected an updated line, got %v", msgs)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].GithubPRNumber == nil || *wts[0].GithubPRNumber != 7 {
		t.Errorf("worktree not linked to PR #7: %+v", wts[0])
	}
	if wts[0].CreatedBy != schema.CreatedByPreexisting {
		t.Errorf("created_by=%q, want preexisting (adoption preserved)", wts[0].CreatedBy)
	}
}

func TestInitRepositoryNotGitHub(t *testing.T) {
	h := newHarness(t)
	h.gh.infoErr = errors.New("not a repo")
	if _, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x")); err == nil {
		t.Error("expected error for non-GitHub repo")
	}
}

func TestInitRepository404GitHubRemoteHintsCredentials(t *testing.T) {
	h := newHarness(t)
	h.gh.infoErr = fmt.Errorf("%w: gh repo view: HTTP 404", github.ErrRepoNotFound)
	h.git.remoteURL = "https://github.com/ceilingfish/lumberjack.git"
	h.gh.user = "work-account"

	_, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x"))
	if err == nil {
		t.Fatal("expected error for inaccessible GitHub repo")
	}
	msg := err.Error()
	for _, want := range []string{"ceilingfish/lumberjack", "work-account", "gh auth switch"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestInitRepository404NonGitHubRemoteFallsBack(t *testing.T) {
	h := newHarness(t)
	h.gh.infoErr = fmt.Errorf("%w: HTTP 404", github.ErrRepoNotFound)
	h.git.remoteURL = "https://gitlab.com/someone/thing.git"

	_, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x"))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "gh auth switch") {
		t.Errorf("non-GitHub remote should not get a credentials hint: %v", err)
	}
}

func TestGithubRepoSlug(t *testing.T) {
	cases := []struct {
		raw      string
		wantSlug string
		wantOK   bool
	}{
		{"https://github.com/ceilingfish/lumberjack.git", "ceilingfish/lumberjack", true},
		{"https://github.com/ceilingfish/lumberjack", "ceilingfish/lumberjack", true},
		{"git@github.com:ceilingfish/lumberjack.git", "ceilingfish/lumberjack", true},
		{"ssh://git@github.com/ceilingfish/lumberjack.git", "ceilingfish/lumberjack", true},
		{"git@github.acme.com:team/repo.git", "team/repo", true},
		{"https://gitlab.com/someone/thing.git", "", false},
		{"git@gitlab.com:someone/thing.git", "", false},
		{"not a url", "", false},
	}
	for _, tc := range cases {
		slug, ok := githubRepoSlug(tc.raw)
		if ok != tc.wantOK || slug != tc.wantSlug {
			t.Errorf("githubRepoSlug(%q) = (%q, %v), want (%q, %v)", tc.raw, slug, ok, tc.wantSlug, tc.wantOK)
		}
	}
}

func TestInitRepositoryNoRemote(t *testing.T) {
	h := newHarness(t)
	h.git.remoteErr = errors.New("no remotes")
	if _, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x")); err == nil {
		t.Error("expected error when repo has no remote")
	}
}

func TestInitRepositoryDuplicate(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.parent, "dup")
	if _, _, err := h.svc.InitRepository(context.Background(), dir); err != nil {
		t.Fatalf("first init: %v", err)
	}
	_, _, err := h.svc.InitRepository(context.Background(), dir)
	if !errors.Is(err, database.ErrRepositoryExists) {
		t.Errorf("expected ErrRepositoryExists, got %v", err)
	}
}

func TestInitRepositoryRecordsLogin(t *testing.T) {
	h := newHarness(t)
	h.gh.active = "work-account"

	repo, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "myrepo"))
	if err != nil {
		t.Fatalf("InitRepository: %v", err)
	}
	if repo.Login != "work-account" {
		t.Errorf("Login = %q, want work-account", repo.Login)
	}
	// It must be persisted, not just returned.
	got, _ := h.db.FindRepository(context.Background(), repo.LocalPath)
	if got.Login != "work-account" {
		t.Errorf("persisted Login = %q, want work-account", got.Login)
	}
}

func TestInitRepositoryActiveLoginErrorAborts(t *testing.T) {
	h := newHarness(t)
	h.gh.activeErr = errors.New("no active account")
	if _, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x")); err == nil {
		t.Error("expected init to fail when the active account can't be determined")
	}
}

// repoWithLogin inserts a tracked repo registered under a given login.
func (h *harness) repoWithLogin(t *testing.T, login string) *schema.Repository {
	t.Helper()
	r := h.repo(t)
	r.Login = login
	if _, err := h.db.NewUpdate().Model(r).Column("login").WherePK().Exec(context.Background()); err != nil {
		t.Fatalf("set login: %v", err)
	}
	return r
}

func TestSetLoginPersistsAndTakesEffect(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t) // no login initially
	h.gh.logins = []string{"personal", "work"}

	updated, err := h.svc.SetLogin(context.Background(), repo, "work")
	if err != nil {
		t.Fatalf("SetLogin: %v", err)
	}
	if updated.Login != "work" {
		t.Errorf("returned Login = %q, want work", updated.Login)
	}
	// Persisted, not just mutated in memory.
	got, _ := h.db.FindRepository(context.Background(), repo.LocalPath)
	if got.Login != "work" {
		t.Errorf("persisted Login = %q, want work", got.Login)
	}

	// A subsequent operation on the reloaded repo now switches accounts.
	h.gh.active = "personal"
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	if _, _, err := h.svc.SyncRepository(context.Background(), got, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if len(h.gh.switches) == 0 || h.gh.switches[0] != [2]string{"github.com", "work"} {
		t.Errorf("expected switch to work, got %v", h.gh.switches)
	}
}

func TestSetLoginEmptyRejected(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if _, err := h.svc.SetLogin(context.Background(), repo, ""); err == nil {
		t.Error("expected an empty login to be rejected")
	}
}

func TestSetLoginUnknownAccountRejected(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.logins = []string{"personal", "work"}

	_, err := h.svc.SetLogin(context.Background(), repo, "ghost")
	if err == nil {
		t.Fatal("expected an unauthenticated login to be rejected")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "personal") {
		t.Errorf("error should name the bad login and the available ones, got %v", err)
	}
	// Nothing persisted.
	got, _ := h.db.FindRepository(context.Background(), repo.LocalPath)
	if got.Login != "" {
		t.Errorf("login should be unchanged, got %q", got.Login)
	}
}

func TestSetLoginUnreachableRepoRejected(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.logins = []string{"personal", "work"}
	h.gh.active = "personal"
	h.gh.accessErr = fmt.Errorf("HTTP 404: Not Found")

	_, err := h.svc.SetLogin(context.Background(), repo, "work")
	if err == nil {
		t.Fatal("expected a login that cannot reach the repo to be rejected")
	}
	if !strings.Contains(err.Error(), "cannot access") {
		t.Errorf("error should explain the access failure, got %v", err)
	}
	// The check must have run under the candidate account, not the prior one.
	if len(h.gh.accessChecks) != 1 || h.gh.accessChecks[0] != "work" {
		t.Errorf("access check ran under %v, want [work]", h.gh.accessChecks)
	}
	// Nothing persisted, and the prior account is restored.
	got, _ := h.db.FindRepository(context.Background(), repo.LocalPath)
	if got.Login != "" {
		t.Errorf("login should be unchanged, got %q", got.Login)
	}
	if h.gh.active != "personal" {
		t.Errorf("active account should be restored to personal, got %q", h.gh.active)
	}
}

func TestListLoginsReportsAccountsAndCurrent(t *testing.T) {
	h := newHarness(t)
	repo := h.repoWithLogin(t, "work")
	h.gh.logins = []string{"personal", "work"}

	logins, err := h.svc.ListLogins(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListLogins: %v", err)
	}
	if len(logins) != 2 || logins[0] != "personal" || logins[1] != "work" {
		t.Errorf("logins = %v, want [personal work]", logins)
	}
}

func TestSyncSwitchesToRepoLoginAndRestores(t *testing.T) {
	h := newHarness(t)
	repo := h.repoWithLogin(t, "work")
	h.gh.active = "personal" // a different account is active
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	// Switched to the repo's login for the operation, then back to personal.
	want := [][2]string{{"github.com", "work"}, {"github.com", "personal"}}
	if fmt.Sprintf("%v", h.gh.switches) != fmt.Sprintf("%v", want) {
		t.Errorf("switches = %v, want %v", h.gh.switches, want)
	}
	if h.gh.active != "personal" {
		t.Errorf("active account not restored: %q", h.gh.active)
	}
}

func TestSyncNoSwitchWhenAlreadyActive(t *testing.T) {
	h := newHarness(t)
	repo := h.repoWithLogin(t, "work")
	h.gh.active = "work" // already the right account
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if len(h.gh.switches) != 0 {
		t.Errorf("expected no account switches, got %v", h.gh.switches)
	}
}

func TestSyncNoSwitchWhenLoginUnset(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t) // empty Login (pre-capture repo)
	h.gh.active = "personal"
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if len(h.gh.switches) != 0 {
		t.Errorf("empty-login repo must not switch accounts, got %v", h.gh.switches)
	}
}

func TestSyncSwitchFailureAborts(t *testing.T) {
	h := newHarness(t)
	repo := h.repoWithLogin(t, "work")
	h.gh.active = "personal"
	h.gh.switchErr = errors.New("account not found")

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err == nil {
		t.Error("expected sync to fail when the account switch fails")
	}
}

func TestDeleteWorktreeSwitchesLogin(t *testing.T) {
	h := newHarness(t)
	repo := h.repoWithLogin(t, "work")
	h.gh.active = "personal"
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	h.gh.switches = nil // ignore the switches from seeding

	if _, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if len(h.gh.switches) != 2 || h.gh.active != "personal" {
		t.Errorf("delete should switch to work then restore personal, switches=%v active=%q", h.gh.switches, h.gh.active)
	}
}

func TestWorktreeViewsSwitchesLogin(t *testing.T) {
	h := newHarness(t)
	repo := h.repoWithLogin(t, "work")
	h.gh.active = "personal"
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	h.gh.switches = nil

	if _, err := h.svc.WorktreeViews(context.Background(), repo); err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	if len(h.gh.switches) != 2 || h.gh.active != "personal" {
		t.Errorf("views should switch to work then restore personal, switches=%v active=%q", h.gh.switches, h.gh.active)
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
	if !containsSubstr(msgs, "a: retained") {
		t.Errorf("expected a per-branch retained line, got %v", msgs)
	}
}

// TestSyncProgressLinesPerBranch checks the per-branch progress vocabulary:
// a new PR reports "checked out"; once its PR closes it reports "deleted".
func TestSyncProgressLinesPerBranch(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	var created []string
	if _, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(m string) { created = append(created, m) }); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !containsSubstr(created, "a: checked out") {
		t.Errorf("expected a checked-out line, got %v", created)
	}

	h.gh.prs = nil
	var deleted []string
	if _, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(m string) { deleted = append(deleted, m) }); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !containsSubstr(deleted, "a: deleted") {
		t.Errorf("expected a deleted line, got %v", deleted)
	}
}

// containsSubstr reports whether any element of msgs contains sub.
func containsSubstr(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
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

func TestSyncAdoptsExistingWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/x"}}

	// The branch is already checked out by hand in a directory git knows about,
	// so recreating it would fail with "a branch named ... already exists".
	existing := filepath.Join(h.parent, "hand-checkout")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{{Dir: existing, Branch: "feature/x"}}
	// Force AddWorktree to fail so the test proves adoption avoids it entirely.
	h.git.addErr["feature/x"] = errors.New("a branch named 'feature/x' already exists")

	var msgs []string
	created, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(m string) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created=%d, want 1 (adopted)", created)
	}
	if !containsSubstr(msgs, "feature/x: adopted") {
		t.Errorf("expected an adopted line, got %v", msgs)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	if wts[0].DirectoryPath != existing {
		t.Errorf("adopted dir=%q, want %q", wts[0].DirectoryPath, existing)
	}
	if wts[0].CreatedBy != schema.CreatedByPreexisting {
		t.Errorf("created_by=%q, want %q", wts[0].CreatedBy, schema.CreatedByPreexisting)
	}
}

func TestSyncDoesNotAdoptTrackedWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	// First sync creates and tracks the worktree.
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)

	// git now reports that worktree; a second sync must not re-adopt it as a
	// second row nor flip its created_by.
	h.git.worktrees = []worktree.Ref{{Dir: wts[0].DirectoryPath, Branch: "a"}}
	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d, want 0", created)
	}
	got, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(got) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(got))
	}
	if got[0].CreatedBy != schema.CreatedByLumberjack {
		t.Errorf("created_by=%q, want %q", got[0].CreatedBy, schema.CreatedByLumberjack)
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
