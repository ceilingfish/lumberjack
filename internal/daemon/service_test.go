package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	dirtyErr  map[string]error
	localOnly map[string]int64
	branches  map[string]string
	addErr    map[string]error // keyed by branch
	removeErr map[string]error
	// newBranches records, per branch, the base AddWorktreeNewBranch created it
	// from; newBranchErr forces that fallback to fail, keyed by branch.
	newBranches    map[string]string
	newBranchErr   map[string]error
	fetchErr       error
	fetchErrByPath map[string]error
	fetchBlock     chan struct{}
	pullErr        error    // forces Pull to fail
	pulled         []string // repo paths Pull was called on, for assertions
	remotes        string
	remoteErr      error
	remoteURL      string
	urlErr         error
	// worktrees is what ListWorktrees reports — the worktrees git already has
	// registered (used to exercise adoption of hand-checked-out directories).
	// listErr forces the listing to fail.
	worktrees []worktree.Ref
	listErr   error
	// defaultBranch is what DefaultBranch reports; defaultBranchErr forces it
	// to fail. configFiles maps "ref:path" to the trusted config content
	// ShowFile serves; a missing entry reports found=false. showFileErr forces
	// ShowFile to fail.
	defaultBranch    string
	defaultBranchErr error
	configFiles      map[string][]byte
	showFileErr      error
	// moveErr forces MoveWorktree to fail for a source directory; moves records
	// the from→to pairs it was asked for.
	moveErr map[string]error
	moves   [][2]string
	// locks is git's lock state, keyed by worktree directory and holding the
	// lock's reason — the same thing `worktree list --porcelain` reports, and
	// what lockWorktrees() turns into the ListWorktrees output. LockWorktree and
	// UnlockWorktree keep it current so tidy's tests can assert a lifted lock was
	// put back (and at the new location). lockErr/unlockErr force those calls to
	// fail, keyed by directory.
	locks     map[string]string
	lockErr   map[string]error
	unlockErr map[string]error
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		dirty:          map[string]bool{},
		dirtyErr:       map[string]error{},
		localOnly:      map[string]int64{},
		branches:       map[string]string{},
		addErr:         map[string]error{},
		removeErr:      map[string]error{},
		fetchErrByPath: map[string]error{},
		newBranches:    map[string]string{},
		newBranchErr:   map[string]error{},
		remotes:        "origin",
		locks:          map[string]string{},
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

func (f *fakeGit) Fetch(_ context.Context, repoPath, _ string) error {
	if f.fetchBlock != nil {
		<-f.fetchBlock
	}
	if err := f.fetchErrByPath[repoPath]; err != nil {
		return err
	}
	return f.fetchErr
}

func (f *fakeGit) Pull(_ context.Context, repoPath string) error {
	f.pulled = append(f.pulled, repoPath)
	return f.pullErr
}

// mkWorktreeDir creates a fake worktree directory with the .git gitdir
// pointer file real worktrees always carry (Reconcile treats a directory
// without one as a husk left by an out-of-band removal).
func mkWorktreeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: fake\n"), 0o644)
}

func (f *fakeGit) AddWorktree(_ context.Context, _, dir, _, branch string) error {
	if err := f.addErr[branch]; err != nil {
		return err
	}
	f.branches[dir] = branch
	return mkWorktreeDir(dir)
}

// AddWorktreeNewBranch records the base each new branch was created from so
// `worktree add` tests can assert the fallback ran and off which ref.
func (f *fakeGit) AddWorktreeNewBranch(_ context.Context, _, dir, base, branch string) error {
	if err := f.newBranchErr[branch]; err != nil {
		return err
	}
	f.newBranches[branch] = base
	f.branches[dir] = branch
	return mkWorktreeDir(dir)
}

func (f *fakeGit) RemoveWorktree(_ context.Context, _, dir string, _ bool) error {
	if err := f.removeErr[dir]; err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// MoveWorktree relocates the directory the way git would, so tidy's tests
// observe the same on-disk outcome. moveErr, keyed by source directory, forces
// a move to fail (git refusing a locked worktree, say); moves records each
// from→to pair for assertions.
func (f *fakeGit) MoveWorktree(_ context.Context, _, from, to string) error {
	if err := f.moveErr[from]; err != nil {
		return err
	}
	f.moves = append(f.moves, [2]string{from, to})
	if b, ok := f.branches[from]; ok {
		delete(f.branches, from)
		f.branches[to] = b
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// ListWorktrees reports the registered worktrees, with git's lock state folded
// in: a locked directory that is not otherwise registered still appears, since
// that is what `worktree list --porcelain` would show and what tidy reads locks
// from.
func (f *fakeGit) ListWorktrees(context.Context, string) ([]worktree.Ref, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	refs := make([]worktree.Ref, 0, len(f.worktrees)+len(f.locks))
	listed := map[string]bool{}
	for _, r := range f.worktrees {
		if reason, ok := f.locks[r.Dir]; ok {
			r.Locked, r.LockReason = true, reason
		}
		listed[r.Dir] = true
		refs = append(refs, r)
	}
	for dir, reason := range f.locks {
		if !listed[dir] {
			refs = append(refs, worktree.Ref{Dir: dir, Locked: true, LockReason: reason})
		}
	}
	return refs, nil
}

// LockWorktree and UnlockWorktree mutate the fake's lock state the way git
// would, so a test can assert where a lifted lock ended up.
func (f *fakeGit) LockWorktree(_ context.Context, _, dir, reason string) error {
	if err := f.lockErr[dir]; err != nil {
		return err
	}
	f.locks[dir] = reason
	return nil
}

func (f *fakeGit) UnlockWorktree(_ context.Context, _, dir string) error {
	if err := f.unlockErr[dir]; err != nil {
		return err
	}
	delete(f.locks, dir)
	return nil
}

func (f *fakeGit) IsDirty(_ context.Context, dir string) (bool, error) {
	if err := f.dirtyErr[dir]; err != nil {
		return false, err
	}
	return f.dirty[dir], nil
}

func (f *fakeGit) LocalOnlyCommits(_ context.Context, dir string) (int64, error) {
	return f.localOnly[dir], nil
}

func (f *fakeGit) CurrentBranch(_ context.Context, dir string) (string, error) {
	if b, ok := f.branches[dir]; ok {
		return b, nil
	}
	for _, r := range f.worktrees {
		if r.Dir == dir {
			return r.Branch, nil
		}
	}
	return "", nil
}

func (f *fakeGit) DefaultBranch(context.Context, string, string) (string, error) {
	if f.defaultBranchErr != nil {
		return "", f.defaultBranchErr
	}
	if f.defaultBranch == "" {
		return "main", nil
	}
	return f.defaultBranch, nil
}

func (f *fakeGit) ShowFile(_ context.Context, _, ref, path string) ([]byte, bool, error) {
	if f.showFileErr != nil {
		return nil, false, f.showFileErr
	}
	data, ok := f.configFiles[ref+":"+path]
	return data, ok, nil
}

// fakeGH satisfies GHOps.
type fakeGH struct {
	info    github.RepoInfo
	infoErr error
	prs     []github.PR
	prsErr  error
	// merged reports, per PR number, whether PRMerged answers true; mergedErr
	// forces the lookup to fail.
	merged    map[int64]bool
	mergedErr error
	user      string
	userErr   error
	// active is the account gh reports as currently signed in; switchErr forces
	// SwitchAccount to fail. switches records each (host, login) switch made.
	active           string
	activeErr        error
	switchErr        error
	switchErrByLogin map[string]error
	switches         [][2]string
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

func (f *fakeGH) PRMerged(_ context.Context, _ github.RepoInfo, number int64) (bool, error) {
	if f.mergedErr != nil {
		return false, f.mergedErr
	}
	return f.merged[number], nil
}

func (f *fakeGH) AuthenticatedUser(context.Context) (string, error) {
	return f.user, f.userErr
}

func (f *fakeGH) ActiveLogin(context.Context, string) (string, error) {
	return f.active, f.activeErr
}

func (f *fakeGH) SwitchAccount(_ context.Context, host, login string) error {
	if err := f.switchErrByLogin[login]; err != nil {
		return err
	}
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
	return h.namedRepo(t, "n")
}

func (h *harness) namedRepo(t *testing.T, name string) *schema.Repository {
	t.Helper()
	r := &schema.Repository{
		LocalPath: filepath.Join(h.parent, name), WorktreeParentDir: h.parent,
		DirPrefix: name, GithubOwner: "o", GithubName: name,
		DefaultRemote: "origin", Host: "github.com",
	}
	if err := h.db.CreateRepository(context.Background(), r); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	return r
}

// seedSync runs an initial reconciliation to establish state for a test,
// failing fast if the setup sync itself errors (so tests fail on their own
// assertions, not a broken fixture).
func (h *harness) seedSync(t *testing.T, repo *schema.Repository) {
	t.Helper()
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("seed SyncRepository: %v", err)
	}
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
	if len(adopted) != 1 || adopted[0].Branch != "feature/x" || adopted[0].Action != ActionAdopted {
		t.Errorf("adopted=%v, want [{feature/x adopted}]", adopted)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	if wts[0].DirectoryPath != existing || wts[0].BranchName != "feature/x" {
		t.Errorf("adopted wrong worktree: %+v", wts[0])
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

	var changes []WorktreeChange
	created, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d, want 0 (linked, not created)", created)
	}
	if !hasAction(changes, "feature/x", ActionUpdated) {
		t.Errorf("expected an updated change, got %v", changes)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].GithubPRNumber == nil || *wts[0].GithubPRNumber != 7 {
		t.Errorf("worktree not linked to PR #7: %+v", wts[0])
	}
}

func TestSyncLinksAdoptedWorktreeAfterBranchRename(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.parent, "n")
	existing := filepath.Join(h.parent, "n-tidy")
	h.git.worktrees = []worktree.Ref{
		{Dir: dir, Branch: "main"},
		{Dir: existing, Branch: "worktree-tidy"},
	}
	repo, adopted, err := h.svc.InitRepository(context.Background(), dir)
	if err != nil || len(adopted) != 1 {
		t.Fatalf("init: adopted=%v err=%v", adopted, err)
	}

	// The branch checked out in the tracked directory is then changed outside
	// Lumberjack, so the stored branch name is stale. A sync seeing an open PR on
	// the branch now checked out there must still link the existing row rather
	// than try to recreate a branch git already has.
	h.git.worktrees[1].Branch = "feature/tidy"
	h.gh.prs = []github.PR{{Number: 28, HeadBranch: "feature/tidy"}}
	h.git.addErr["feature/tidy"] = errors.New("a branch named 'feature/tidy' already exists")

	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d, want 0 (linked, not created)", created)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].GithubPRNumber == nil || *wts[0].GithubPRNumber != 28 {
		t.Errorf("worktree not linked to PR #28: %+v", wts[0])
	}
	if wts[0].BranchName != "feature/tidy" {
		t.Errorf("branch_name=%q, want the branch actually checked out", wts[0].BranchName)
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
		{"https://github.com/ceilingfish", "", false},
		{"https://github.com/", "", false},
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
	if !reflect.DeepEqual(h.gh.switches, want) {
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

	var changes []WorktreeChange
	created, removed, err := h.svc.SyncRepository(context.Background(), repo, func(c WorktreeChange) { changes = append(changes, c) })
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
	if len(changes) == 0 {
		t.Error("expected progress changes")
	}
}

func TestSyncStampsWorktreeLastSyncedAt(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, err := h.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].LastSyncedAt == nil {
		t.Error("worktree LastSyncedAt is nil after a successful sync, want it set")
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
	h.seedSync(t, repo)

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
	h.seedSync(t, repo)

	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	// Mark the worktree dirty and its PR closed.
	h.git.dirty[wts[0].DirectoryPath] = true
	h.gh.prs = nil

	var changes []WorktreeChange
	_, removed, err := h.svc.SyncRepository(context.Background(), repo, func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0 (retained)", removed)
	}
	if got, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(got) != 1 {
		t.Errorf("expected worktree retained, got %d", len(got))
	}
	if !hasAction(changes, "a", ActionRetained) {
		t.Errorf("expected a per-branch retained change, got %v", changes)
	}
}

// TestSyncKeepsWorktreeWithNoPR checks a clean worktree that has no associated
// PR is never auto-removed — without a finished PR there is nothing to say its
// work is safe to discard.
func TestSyncKeepsWorktreeWithNoPR(t *testing.T) {
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

	// A sync with no open PR on that branch must keep it (clean, but no PR).
	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0 (no PR is not a cleanup candidate)", removed)
	}
	if got, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(got) != 1 {
		t.Errorf("expected worktree kept, got %d", len(got))
	}
}

// TestSyncRemovesMergedWorktreeWithLocalCommits covers the squash-merge case:
// a worktree carrying commits that sit on no remote-tracking ref is still
// removed when its PR merged, because those commits are on the base branch.
func TestSyncRemovesMergedWorktreeWithLocalCommits(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)

	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	// Commits present on no remote ref (as after a squash merge), but the PR
	// was merged — so they are not at risk and the worktree may be removed.
	h.git.localOnly[wts[0].DirectoryPath] = 4
	h.gh.prs = nil
	h.gh.merged = map[int64]bool{1: true}

	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed=%d, want 1 (merged worktree is safe to remove)", removed)
	}
	if got, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(got) != 0 {
		t.Errorf("expected merged worktree removed, got %d", len(got))
	}
}

// TestSyncPullsCleanRepo checks sync fast-forwards the main checkout when clean
// and leaves it alone when it has uncommitted changes.
func TestSyncPullsCleanRepo(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(h.git.pulled) != 1 || h.git.pulled[0] != repo.LocalPath {
		t.Errorf("expected a pull of %s, got %v", repo.LocalPath, h.git.pulled)
	}

	// A dirty main checkout must not be pulled.
	h.git.pulled = nil
	h.git.dirty[repo.LocalPath] = true
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(h.git.pulled) != 0 {
		t.Errorf("dirty checkout should not be pulled, got %v", h.git.pulled)
	}
}

// TestSyncProgressChangesPerBranch checks the per-branch action vocabulary:
// a new PR reports "checked out"; once its PR closes it reports "deleted".
func TestSyncProgressChangesPerBranch(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	var created []WorktreeChange
	if _, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { created = append(created, c) }); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !hasAction(created, "a", ActionCheckedOut) {
		t.Errorf("expected a checked-out change, got %v", created)
	}

	h.gh.prs = nil
	var deleted []WorktreeChange
	if _, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { deleted = append(deleted, c) }); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !hasAction(deleted, "a", ActionDeleted) {
		t.Errorf("expected a deleted change, got %v", deleted)
	}
}

// hasAction reports whether changes contains one for branch with the given action.
func hasAction(changes []WorktreeChange, branch string, action WorktreeAction) bool {
	for _, c := range changes {
		if c.Branch == branch && c.Action == action {
			return true
		}
	}
	return false
}

func TestSyncPrunesMissingWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)

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

	var changes []WorktreeChange
	created, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created=%d, want 1 (adopted)", created)
	}
	if !hasAction(changes, "feature/x", ActionAdopted) {
		t.Errorf("expected an adopted change, got %v", changes)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	if wts[0].DirectoryPath != existing {
		t.Errorf("adopted dir=%q, want %q", wts[0].DirectoryPath, existing)
	}
}

// A PR whose branch the main checkout has checked out cannot get a worktree —
// git allows a branch in only one working tree — so sync reports it and carries
// on rather than failing with "a branch named ... already exists" every loop.
func TestSyncSkipsPROnMainCheckoutBranch(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 910, HeadBranch: "fix/x"}, {Number: 911, HeadBranch: "feature/y"}}
	h.git.worktrees = []worktree.Ref{{Dir: repo.LocalPath, Branch: "fix/x"}}
	// Creation must never be attempted for the main checkout's branch.
	h.git.addErr["fix/x"] = errors.New("a branch named 'fix/x' already exists")

	var changes []WorktreeChange
	created, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created=%d, want 1 (only the other PR)", created)
	}
	if !hasAction(changes, "fix/x", ActionRetained) {
		t.Errorf("expected fix/x reported as retained, got %v", changes)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 || wts[0].BranchName != "feature/y" {
		t.Fatalf("expected only feature/y tracked, got %+v", wts)
	}
}

func (h *harness) syncedWorktree(t *testing.T, repo *schema.Repository) schema.Worktree {
	t.Helper()
	wts, err := h.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	return wts[0]
}

func TestWorktreeViewsFlagsBranchDisparity(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	wt := h.syncedWorktree(t, repo)
	h.git.branches[wt.DirectoryPath] = "feature/elsewhere"

	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	st := views[0].Status
	if !st.BranchDisparity || !st.NeedsReconciliation {
		t.Errorf("expected a flagged disparity: %+v", st)
	}
	if st.CheckedOutBranch != "feature/elsewhere" {
		t.Errorf("CheckedOutBranch = %q", st.CheckedOutBranch)
	}
	want := "needs reconciliation: disparity between local branch feature/elsewhere and PR branch feature/x"
	if st.Note != want {
		t.Errorf("note = %q, want %q", st.Note, want)
	}
}

func TestSyncRetainsWorktreeWithBranchDisparity(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	wt := h.syncedWorktree(t, repo)
	h.git.branches[wt.DirectoryPath] = "feature/elsewhere"

	h.gh.prs = nil
	h.gh.merged = map[int64]bool{4: true}

	var changes []WorktreeChange
	_, removed, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0: a disparity is never cleaned up", removed)
	}
	if !hasAction(changes, "feature/x", ActionRetained) {
		t.Errorf("expected feature/x retained, got %v", changes)
	}
	if _, err := os.Stat(wt.DirectoryPath); err != nil {
		t.Errorf("worktree directory must survive: %v", err)
	}
	if got := h.syncedWorktree(t, repo); got.ID != wt.ID {
		t.Errorf("expected the worktree to stay tracked, got %+v", got)
	}
}

func TestSyncSuppressesPRWhoseBranchAWorktreeHolds(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	wt := h.syncedWorktree(t, repo)

	h.git.branches[wt.DirectoryPath] = "feature/y"
	h.git.worktrees = []worktree.Ref{
		{Dir: repo.LocalPath, Branch: "main"},
		{Dir: wt.DirectoryPath, Branch: "feature/y"},
	}
	h.gh.prs = append(h.gh.prs, github.PR{Number: 5, HeadBranch: "feature/y"})
	h.git.addErr["feature/y"] = errors.New("a branch named 'feature/y' already exists")

	var changes []WorktreeChange
	created, _, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 0 {
		t.Errorf("created=%d, want 0: the branch is checked out elsewhere", created)
	}
	if !hasAction(changes, "feature/y", ActionRetained) {
		t.Errorf("expected feature/y retained, got %v", changes)
	}
	if got := h.syncedWorktree(t, repo); got.ID != wt.ID {
		t.Errorf("expected only the original worktree tracked, got %+v", got)
	}

	h.git.branches[wt.DirectoryPath] = "feature/x"
	h.git.worktrees = []worktree.Ref{
		{Dir: repo.LocalPath, Branch: "main"},
		{Dir: wt.DirectoryPath, Branch: "feature/x"},
	}
	delete(h.git.addErr, "feature/y")

	created, _, err = h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created=%d, want 1 once the disparity is resolved", created)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 2 {
		t.Fatalf("expected 2 tracked worktrees, got %d", len(wts))
	}
}

func TestSyncCreatesWorktreeAfterDisparateWorktreeDeleted(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 4, HeadBranch: "feature/x"}}
	h.seedSync(t, repo)
	wt := h.syncedWorktree(t, repo)

	h.git.branches[wt.DirectoryPath] = "feature/y"
	h.git.worktrees = []worktree.Ref{{Dir: wt.DirectoryPath, Branch: "feature/y"}}
	h.gh.prs = append(h.gh.prs, github.PR{Number: 5, HeadBranch: "feature/y"})

	res, err := h.svc.DeleteWorktree(context.Background(), repo, "feature/x", false)
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !res.RequiresConfirmation {
		t.Fatalf("a disparity must be confirmed before deletion: %+v", res)
	}
	want := "worktree is checked out on feature/y rather than its PR branch"
	if res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
	if _, err := h.svc.DeleteWorktree(context.Background(), repo, "feature/x", true); err != nil {
		t.Fatalf("forced DeleteWorktree: %v", err)
	}

	h.git.worktrees = nil
	delete(h.git.branches, wt.DirectoryPath)
	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("sync after delete: %v", err)
	}
	if created != 2 {
		t.Errorf("created=%d, want 2 (both PRs get a worktree again)", created)
	}
}

// An on-disk worktree no open PR claims is still adopted, so a directory
// created by hand after `init` does not stay invisible to Lumberjack.
func TestSyncAdoptsOrphanWorktreeWithNoPR(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = nil // no open PRs at all

	orphan := filepath.Join(h.parent, "hand-checkout")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{
		{Dir: repo.LocalPath, Branch: "main"}, // the main checkout is never adopted
		{Dir: orphan, Branch: "feature/orphan"},
	}

	var changes []WorktreeChange
	created, removed, err := h.svc.SyncRepository(context.Background(), repo,
		func(c WorktreeChange) { changes = append(changes, c) })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if created != 1 || removed != 0 {
		t.Errorf("created=%d removed=%d, want 1 and 0", created, removed)
	}
	if !hasAction(changes, "feature/orphan", ActionAdopted) {
		t.Errorf("expected an adopted change for the orphan, got %v", changes)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	if wts[0].DirectoryPath != orphan || wts[0].BranchName != "feature/orphan" {
		t.Errorf("adopted wrong worktree: %+v", wts[0])
	}
	if wts[0].GithubPRNumber != nil {
		t.Errorf("orphan should have no PR number, got %v", *wts[0].GithubPRNumber)
	}

	// A second sync must not re-adopt it, and must not delete it either: with no
	// PR recorded it is tracked, not managed.
	created, removed, err = h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if created != 0 || removed != 0 {
		t.Errorf("second sync created=%d removed=%d, want 0 and 0", created, removed)
	}
	if got, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(got) != 1 {
		t.Fatalf("expected the orphan to stay tracked once, got %d", len(got))
	}
}

// An orphan adopted with no PR is linked to a PR later opened on its branch,
// rather than being re-adopted as a second row.
func TestSyncLinksOrphanToLaterPR(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)

	orphan := filepath.Join(h.parent, "hand-checkout")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{{Dir: orphan, Branch: "feature/x"}}
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// A PR now exists for the orphan's branch. Recreating the worktree would
	// fail, so linking is the only way through.
	h.gh.prs = []github.PR{{Number: 7, HeadBranch: "feature/x"}}
	h.git.addErr["feature/x"] = errors.New("a branch named 'feature/x' already exists")
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 tracked worktree, got %d", len(wts))
	}
	if wts[0].GithubPRNumber == nil || *wts[0].GithubPRNumber != 7 {
		t.Errorf("expected the orphan linked to PR #7, got %+v", wts[0])
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
	// second row.
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
	h.seedSync(t, repo)

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
	h.seedSync(t, repo)

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
	h.seedSync(t, repo)
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

// TestDeleteMergedWorktreeNoConfirmation checks a merged PR's worktree deletes
// without warning even when it holds commits on no remote ref — they are on the
// base branch, so nothing is at risk.
func TestDeleteMergedWorktreeNoConfirmation(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	h.git.localOnly[wts[0].DirectoryPath] = 4
	h.gh.merged = map[int64]bool{1: true}

	res, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !res.Deleted || res.RequiresConfirmation || res.CommitsAtRisk != 0 {
		t.Errorf("merged worktree should delete cleanly: %+v", res)
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
	h.seedSync(t, repo)
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
	h.seedSync(t, repo)
	run, _ := h.db.LatestSyncRun(context.Background(), repo.ID)
	if run == nil || !run.StartedAt.Equal(fixed) {
		t.Errorf("expected injected time, got %+v", run)
	}
}

// TestSyncRepositoryPublishesEvents checks the ordering of events a sync
// publishes: a SyncStarted first, one WorktreeChanged per created worktree,
// and a SyncFinished last carrying the outcome.
func TestSyncRepositoryPublishesEvents(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	ch, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil || created != 1 {
		t.Fatalf("SyncRepository: created=%d err=%v", created, err)
	}

	first := recv(t, ch)
	if first.Type != EventSyncStarted {
		t.Fatalf("first event = %v, want EventSyncStarted", first.Type)
	}

	change := recv(t, ch)
	if change.Type != EventWorktreeChanged || change.Change == nil ||
		change.Change.Action != ActionCheckedOut || change.Change.Branch != "a" {
		t.Errorf("worktree change event = %+v", change)
	}
	if change.Change.DirectoryPath == "" {
		t.Errorf("expected a directory path on the change event")
	}

	last := recv(t, ch)
	if last.Type != EventSyncFinished || last.SyncCreated != 1 || last.SyncErr != nil {
		t.Errorf("final event = %+v", last)
	}
}

// TestDeleteWorktreePublishesEvent checks that a direct DeleteWorktree call
// (outside of Sync) also publishes a WorktreeChanged/deleted event.
func TestDeleteWorktreePublishesEvent(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)

	ch, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	res, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err != nil || !res.Deleted {
		t.Fatalf("DeleteWorktree: res=%+v err=%v", res, err)
	}

	ev := recv(t, ch)
	if ev.Type != EventWorktreeChanged || ev.Change == nil || ev.Change.Action != ActionDeleted || ev.Change.Branch != "a" {
		t.Errorf("delete event = %+v", ev)
	}
}

// TestSubscribeMultipleConcurrentSubscribers checks that two subscribers each
// independently receive the same events.
func TestSubscribeMultipleConcurrentSubscribers(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	ch1, unsub1 := h.svc.Subscribe()
	defer unsub1()
	ch2, unsub2 := h.svc.Subscribe()
	defer unsub2()

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}

	for _, ch := range []<-chan Event{ch1, ch2} {
		if ev := recv(t, ch); ev.Type != EventSyncStarted {
			t.Errorf("expected EventSyncStarted, got %v", ev.Type)
		}
	}
}

func TestInitRepositoryReportsAnUnlistableWorktreeSet(t *testing.T) {
	h := newHarness(t)
	h.git.listErr = errors.New("fatal: not a git repository")

	repo, adopted, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x"))
	if err == nil {
		t.Fatal("expected an error when git cannot list worktrees")
	}
	if repo == nil || len(adopted) != 0 {
		t.Errorf("repo=%v adopted=%v, want the repo and no adoptions", repo, adopted)
	}
}

func TestInitRepositoryCredentialHintFallsBackWithoutARemoteURL(t *testing.T) {
	h := newHarness(t)
	h.gh.infoErr = github.ErrRepoNotFound
	h.git.urlErr = errors.New("fatal: no such remote")

	_, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "is not a GitHub repository") {
		t.Errorf("err = %v, want the generic not-a-GitHub-repository message", err)
	}
}

func TestInitRepositoryCredentialHintWithoutAKnownAccount(t *testing.T) {
	h := newHarness(t)
	h.gh.infoErr = github.ErrRepoNotFound
	h.git.remoteURL = "git@github.com:someone/private.git"
	h.gh.userErr = errors.New("gh: not logged in")

	_, _, err := h.svc.InitRepository(context.Background(), filepath.Join(h.parent, "x"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "the current gh credentials may not have access") {
		t.Errorf("err = %v, want the anonymous credentials hint", err)
	}
}

func TestSetLoginReportsWhenGHAccountsCannotBeListed(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.loginsErr = errors.New("gh: not logged in")

	if _, err := h.svc.SetLogin(context.Background(), repo, "octocat"); err == nil {
		t.Error("expected an error when gh's accounts cannot be listed")
	}
}

func TestSetLoginWithNoAuthenticatedAccountsListsNone(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.logins = nil

	_, err := h.svc.SetLogin(context.Background(), repo, "octocat")
	if err == nil {
		t.Fatal("expected an error when gh has no accounts")
	}
	if !strings.Contains(err.Error(), "available: none") {
		t.Errorf("err = %v, want it to report no available accounts", err)
	}
}

func TestSyncReportsAnUnreachableActiveAccount(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "octocat"
	h.gh.activeErr = errors.New("gh: not logged in")

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err == nil {
		t.Error("expected an error when gh's active account cannot be read")
	}
}

func TestSyncSurfacesAFailedAccountRestore(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "octocat"
	h.gh.active = "someone-else"
	h.gh.switchErrByLogin = map[string]error{"someone-else": errors.New("gh: no such account")}

	_, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil {
		t.Fatal("expected the restore failure to surface")
	}
	if !strings.Contains(err.Error(), "restoring GitHub account") {
		t.Errorf("err = %v, want a restore failure", err)
	}
}

func TestSyncReportsWhenOpenPRsCannotBeListed(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prsErr = errors.New("gh: API rate limit exceeded")

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err == nil {
		t.Error("expected an error when open PRs cannot be listed")
	}
}

func TestSyncCollectsAWorktreeListingFailureAndStillCreates(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.git.listErr = errors.New("fatal: not a git repository")

	_, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil || !strings.Contains(err.Error(), "listing existing worktrees") {
		t.Errorf("err = %v, want the listing failure collected", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Errorf("worktrees = %d, want 1 created despite the listing failure", len(wts))
	}
}

func TestSyncCollectsAPRStateLookupFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)

	h.gh.prs = nil
	h.gh.mergedErr = errors.New("gh: API rate limit exceeded")

	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil || !strings.Contains(err.Error(), "resolving PR state") {
		t.Errorf("err = %v, want the PR state failure collected", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 — an unknown PR state must not delete work", removed)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Errorf("worktrees = %d, want the worktree kept", len(wts))
	}
}

func TestSyncCollectsAReconcileFailureAndKeepsTheWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("seed: worktrees = %d, want 1", len(wts))
	}
	h.gh.prs = nil
	h.git.dirtyErr[wts[0].DirectoryPath] = errors.New("fatal: unable to read index")

	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil || !strings.Contains(err.Error(), "reconciling") {
		t.Errorf("err = %v, want the reconcile failure collected", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestSyncCollectsAWorktreeRemovalFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("seed: worktrees = %d, want 1", len(wts))
	}
	h.gh.prs = nil
	h.git.removeErr[wts[0].DirectoryPath] = errors.New("fatal: worktree is locked")

	_, removed, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err == nil || !strings.Contains(err.Error(), "removing") {
		t.Errorf("err = %v, want the removal failure collected", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if wts, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(wts) != 1 {
		t.Errorf("worktrees = %d, want the row kept", len(wts))
	}
}

func TestSyncPullSkippedWhenTheCheckoutCannotBeInspected(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.dirtyErr[repo.LocalPath] = errors.New("fatal: unable to read index")

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if len(h.git.pulled) != 0 {
		t.Errorf("pulled = %v, want no pull attempted", h.git.pulled)
	}
}

func TestSyncPullFailureDoesNotFailTheSync(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.pullErr = errors.New("fatal: not possible to fast-forward")
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	created, _, err := h.svc.SyncRepository(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}
}

func TestWorktreeViewsReportsAFetchFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.fetchErr = errors.New("network is unreachable")

	if _, err := h.svc.WorktreeViews(context.Background(), repo); err == nil {
		t.Error("expected an error when the fetch fails")
	}
}

func TestWorktreeViewsReportsAPRStateFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	h.gh.prs = nil
	h.gh.mergedErr = errors.New("gh: API rate limit exceeded")

	_, err := h.svc.WorktreeViews(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "resolving PR state") {
		t.Errorf("err = %v, want the PR state failure", err)
	}
}

func TestWorktreeViewsReportsAReconcileFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("seed: worktrees = %d, want 1", len(wts))
	}
	h.git.dirtyErr[wts[0].DirectoryPath] = errors.New("fatal: unable to read index")

	_, err := h.svc.WorktreeViews(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "reconciling") {
		t.Errorf("err = %v, want the reconcile failure", err)
	}
}

func TestDeleteWorktreeFetchFailureAborts(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	h.git.fetchErr = errors.New("network is unreachable")

	if _, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false); err == nil {
		t.Error("expected an error when the fetch fails")
	}
	if wts, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(wts) != 1 {
		t.Errorf("worktrees = %d, want the worktree kept", len(wts))
	}
}

func TestDeleteWorktreePRStateFailureAborts(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	h.gh.mergedErr = errors.New("gh: API rate limit exceeded")

	_, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err == nil || !strings.Contains(err.Error(), "checking PR #1 state") {
		t.Errorf("err = %v, want the PR state failure", err)
	}
}

func TestDeleteWorktreeReconcileFailureAborts(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("seed: worktrees = %d, want 1", len(wts))
	}
	h.git.dirtyErr[wts[0].DirectoryPath] = errors.New("fatal: unable to read index")

	_, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err == nil || !strings.Contains(err.Error(), "reconciling") {
		t.Errorf("err = %v, want the reconcile failure", err)
	}
}

func TestDeleteWorktreeRemovalFailureKeepsTheRow(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("seed: worktrees = %d, want 1", len(wts))
	}
	h.git.removeErr[wts[0].DirectoryPath] = errors.New("fatal: worktree is locked")

	_, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false)
	if err == nil || !strings.Contains(err.Error(), "removing") {
		t.Errorf("err = %v, want the removal failure", err)
	}
	if wts, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(wts) != 1 {
		t.Errorf("worktrees = %d, want the row kept when git refused", len(wts))
	}
}

func TestConfirmMessage(t *testing.T) {
	cases := []struct {
		st   worktree.Status
		want string
	}{
		{
			worktree.Status{Dirty: true, LocalOnlyCommits: 2},
			"worktree has uncommitted changes and 2 local-only commit(s) that will be lost",
		},
		{
			worktree.Status{Dirty: true},
			"worktree has uncommitted changes that will be lost",
		},
		{
			worktree.Status{LocalOnlyCommits: 3},
			"worktree has 3 local-only commit(s) that will be lost",
		},
		{
			worktree.Status{BranchDisparity: true, CheckedOutBranch: "other"},
			"worktree is checked out on other rather than its PR branch",
		},
		{
			worktree.Status{Dirty: true, BranchDisparity: true, CheckedOutBranch: "other"},
			"worktree has uncommitted changes that will be lost, and is checked out on other rather than its PR branch",
		},
	}
	for _, c := range cases {
		if got := confirmMessage(c.st); got != c.want {
			t.Errorf("confirmMessage(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
}
