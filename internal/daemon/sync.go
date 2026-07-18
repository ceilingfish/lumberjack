package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// GitOps is the git surface the sync engine needs. *worktree.Git satisfies it;
// the interface exists so the engine is unit-testable with a fake git.
type GitOps interface {
	worktree.Prober
	DefaultRemote(ctx context.Context, repoPath string) (string, error)
	RemoteURL(ctx context.Context, repoPath, remote string) (string, error)
	Fetch(ctx context.Context, repoPath, remote string) error
	AddWorktree(ctx context.Context, repoPath, dir, remote, branch string) error
	RemoveWorktree(ctx context.Context, repoPath, dir string, force bool) error
	// ListWorktrees enumerates the worktrees already registered on the repo,
	// so sync can adopt directories checked out by hand instead of failing to
	// recreate them.
	ListWorktrees(ctx context.Context, repoPath string) ([]worktree.Ref, error)
}

// GHOps is the gh surface the sync engine and init need.
type GHOps interface {
	RepoInfo(ctx context.Context, dir string) (github.RepoInfo, error)
	ListOpenPRs(ctx context.Context, repo github.RepoInfo) ([]github.PR, error)
	// AuthenticatedUser names the account gh is signed in as, so init can point
	// at credentials when a repository is not accessible.
	AuthenticatedUser(ctx context.Context) (string, error)
	// ActiveLogin reports the account gh has active for a host; SwitchAccount
	// changes it. Together they let the daemon operate on a repo under the
	// account it was registered with and restore the prior account afterwards.
	ActiveLogin(ctx context.Context, host string) (string, error)
	SwitchAccount(ctx context.Context, host, login string) error
	// ListLogins reports every gh account authenticated for a host — the logins
	// set-login accepts and the picker offers.
	ListLogins(ctx context.Context, host string) ([]string, error)
	// CheckRepoAccess verifies gh's active account can reach a repository, so
	// set-login can reject a login that authenticates but cannot operate the repo.
	CheckRepoAccess(ctx context.Context, repo github.RepoInfo) error
}

// Service is the daemon's domain layer: it owns every worktree mutation
// (init, sync, delete) by orchestrating the database, git, and gh packages.
// Only the daemon constructs one, so there is a single writer.
type Service struct {
	db  *database.Client
	git GitOps
	gh  GHOps
	now func() time.Time
	// mu serialises worktree mutations so the hourly loop and an on-demand
	// Sync/Delete RPC can never operate on the trees at the same time. The
	// daemon is the single writer; this keeps that guarantee within it too.
	mu sync.Mutex
}

// NewService constructs the daemon domain Service. fx supplies the concrete
// dependencies.
func NewService(db *database.Client, git GitOps, gh GHOps) *Service {
	return &Service{db: db, git: git, gh: gh, now: time.Now}
}

// WorktreeAction is what a sync did to one branch's worktree — the domain
// mirror of the lumberjack.v1.WorktreeAction enum, reported per branch so the
// CLI can render a branch/PR/action table.
type WorktreeAction string

// The WorktreeAction values, one per thing a sync/init can do to a worktree.
const (
	ActionCheckedOut WorktreeAction = "checked out"
	ActionAdopted    WorktreeAction = "adopted"
	ActionUpdated    WorktreeAction = "updated"
	ActionDeleted    WorktreeAction = "deleted"
	ActionRetained   WorktreeAction = "retained"
)

// WorktreeChange is one per-branch progress event: what happened to a branch's
// worktree, the PR it relates to (nil when none, e.g. adoption before its PR is
// known), and an optional detail (e.g. why a worktree was retained).
type WorktreeChange struct {
	Branch   string
	PRNumber *int64
	Action   WorktreeAction
	Detail   string
}

// progressFn receives per-branch worktree changes during a sync. It may be nil.
type progressFn func(WorktreeChange)

func (p progressFn) send(c WorktreeChange) {
	if p != nil {
		p(c)
	}
}

// WorktreeView pairs a stored worktree with its live reconciliation status and
// whether its source PR is still open.
type WorktreeView struct {
	Worktree schema.Worktree
	Status   worktree.Status
	PROpen   bool
}

// repoInfo builds the gh identity from a stored repository row.
func repoInfo(repo *schema.Repository) github.RepoInfo {
	return github.RepoInfo{Owner: repo.GithubOwner, Name: repo.GithubName, Host: repo.Host}
}

// withRepoLogin runs fn with gh's active account switched to the one the
// repository was registered under, restoring the previously-active account
// afterwards. Both git (via gh's credential helper) and gh calls inherit the
// active account, so any operation on a repo must run under its own login.
//
// Repos tracked before login capture (empty Login) run fn unchanged. gh's
// active account is process-global, so callers must hold s.mu.
func (s *Service) withRepoLogin(ctx context.Context, repo *schema.Repository, fn func() error) error {
	return s.withLogin(ctx, repo.Host, repo.Login, fn)
}

// withLogin runs fn with gh's active account switched to login for host,
// restoring the previously-active account afterwards. An empty login (or one
// already active) runs fn without switching. gh's active account is
// process-global, so callers must hold s.mu.
func (s *Service) withLogin(ctx context.Context, host, login string, fn func() error) (err error) {
	if login == "" {
		return fn()
	}
	current, aerr := s.gh.ActiveLogin(ctx, host)
	if aerr != nil {
		return fmt.Errorf("checking active GitHub account: %w", aerr)
	}
	if current == login {
		return fn()
	}
	if serr := s.gh.SwitchAccount(ctx, host, login); serr != nil {
		return fmt.Errorf("switching to GitHub account %q: %w", login, serr)
	}
	defer func() {
		// Restore the account that was active before. A restore failure must not
		// mask fn's own error, but is surfaced when fn otherwise succeeded.
		if serr := s.gh.SwitchAccount(ctx, host, current); serr != nil && err == nil {
			err = fmt.Errorf("restoring GitHub account %q: %w", current, serr)
		}
	}()
	return fn()
}

// fetchOpenPRs fetches remote refs and returns the open PRs indexed by number.
func (s *Service) fetchOpenPRs(ctx context.Context, repo *schema.Repository) (map[int64]github.PR, error) {
	if err := s.git.Fetch(ctx, repo.LocalPath, repo.DefaultRemote); err != nil {
		return nil, fmt.Errorf("fetching %s: %w", repo.DefaultRemote, err)
	}
	prs, err := s.gh.ListOpenPRs(ctx, repoInfo(repo))
	if err != nil {
		return nil, fmt.Errorf("listing open PRs: %w", err)
	}
	byNum := make(map[int64]github.PR, len(prs))
	for _, pr := range prs {
		byNum[pr.Number] = pr
	}
	return byNum, nil
}

// WorktreeViews returns the live view of a repository's tracked worktrees:
// each stored row plus its reconciliation status, computed fresh from git and
// gh (never cached — docs/schema.md).
func (s *Service) WorktreeViews(ctx context.Context, repo *schema.Repository) ([]WorktreeView, error) {
	// Serialise with worktree mutations: gh account switching is process-global,
	// so a concurrent sync must not change the active account mid-read.
	s.mu.Lock()
	defer s.mu.Unlock()

	var views []WorktreeView
	err := s.withRepoLogin(ctx, repo, func() error {
		openByNum, err := s.fetchOpenPRs(ctx, repo)
		if err != nil {
			return err
		}
		stored, err := s.db.ListWorktrees(ctx, repo.ID)
		if err != nil {
			return err
		}
		views = make([]WorktreeView, 0, len(stored))
		for i := range stored {
			wt := stored[i]
			open := wt.GithubPRNumber != nil
			if open {
				_, open = openByNum[*wt.GithubPRNumber]
			}
			st, rerr := worktree.Reconcile(ctx, s.git, wt.DirectoryPath, open)
			if rerr != nil {
				return fmt.Errorf("reconciling %s: %w", wt.DirectoryPath, rerr)
			}
			views = append(views, WorktreeView{Worktree: wt, Status: st, PROpen: open})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return views, nil
}

// SyncRepository reconciles one repository: it creates worktrees for open PRs
// that lack one and removes worktrees whose PR has closed, retaining any that
// still hold un-pushed local work. It records a sync_runs audit entry and
// updates the repository's last-sync fields. Per-PR failures are collected and
// returned as a combined error without aborting the whole sync.
func (s *Service) SyncRepository(ctx context.Context, repo *schema.Repository, progress progressFn) (created, removed int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err = s.withRepoLogin(ctx, repo, func() error {
		var serr error
		created, removed, serr = s.syncRepositoryLocked(ctx, repo, progress)
		return serr
	})
	return created, removed, err
}

// syncRepositoryLocked is the body of SyncRepository, run under s.mu with gh's
// active account already switched to the repository's login.
func (s *Service) syncRepositoryLocked(ctx context.Context, repo *schema.Repository, progress progressFn) (created, removed int, err error) {
	start := s.now()
	run, runErr := s.db.StartSyncRun(ctx, repo.ID, start)
	if runErr != nil {
		return 0, 0, runErr
	}
	// Always close out the audit entry and repo status, even on early return.
	defer func() {
		finish := s.now()
		if fErr := s.db.FinishSyncRun(ctx, run, finish, created, removed, err); fErr != nil && err == nil {
			err = fErr
		}
		if uErr := s.db.UpdateSyncResult(ctx, repo.ID, finish, err); uErr != nil && err == nil {
			err = uErr
		}
	}()

	openByNum, ferr := s.fetchOpenPRs(ctx, repo)
	if ferr != nil {
		err = ferr
		return created, removed, err
	}

	tracked := make([]database.TrackedPR, 0, len(openByNum))
	for _, pr := range openByNum {
		tracked = append(tracked, database.TrackedPR{Number: pr.Number, Branch: pr.HeadBranch})
	}
	if perr := s.db.ReplaceOpenPRs(ctx, repo.ID, tracked, start); perr != nil {
		err = perr
		return created, removed, err
	}

	stored, lerr := s.db.ListWorktrees(ctx, repo.ID)
	if lerr != nil {
		err = lerr
		return created, removed, err
	}

	var errs []error
	created += s.createMissing(ctx, repo, openByNum, stored, progress, &errs)
	removed += s.removeClosed(ctx, repo, openByNum, stored, progress, &errs)

	err = errors.Join(errs...)
	return created, removed, err
}

// reconcileState carries the mutable lookups createMissing threads through each
// PR: which directories are taken, which tracked-but-unlinked branches remain,
// and which on-disk directories are available to adopt (keyed by branch).
type reconcileState struct {
	usedDirs  map[string]bool
	unlinked  map[string]schema.Worktree
	adoptable map[string]string
}

// createMissing gives every open PR that lacks a linked worktree one, returning
// the number of worktrees created or adopted (linking an existing row is not
// counted).
func (s *Service) createMissing(
	ctx context.Context, repo *schema.Repository, openByNum map[int64]github.PR,
	stored []schema.Worktree, progress progressFn, errs *[]error,
) (created int) {
	havePR := make(map[int64]bool, len(stored))
	st := reconcileState{
		usedDirs: make(map[string]bool, len(stored)),
		// Worktrees tracked by branch but not yet linked to a PR (e.g. adopted at
		// init with no PR number). An open PR on such a branch links to the
		// existing row instead of trying to recreate a branch git already has.
		unlinked: make(map[string]schema.Worktree, len(stored)),
	}
	for _, wt := range stored {
		if wt.GithubPRNumber != nil {
			havePR[*wt.GithubPRNumber] = true
		} else {
			st.unlinked[wt.BranchName] = wt
		}
		st.usedDirs[wt.DirectoryPath] = true
	}
	// Directories already checked out on disk but not yet tracked, keyed by
	// branch — hand-created worktrees (or ones a previous run left behind) that
	// we adopt instead of failing to recreate their branch.
	st.adoptable = s.adoptableWorktrees(ctx, repo, st.usedDirs, errs)

	for num, pr := range openByNum {
		if havePR[num] {
			continue
		}
		created += s.reconcilePR(ctx, repo, num, pr, &st, progress, errs)
	}
	return created
}

// reconcilePR ensures one open PR that lacks a linked worktree gets one, in
// preference order: link an already-tracked branch, adopt an untracked
// checked-out directory, or create a fresh worktree. It returns 1 when a
// worktree was created or adopted (linking mutates an existing row and returns
// 0), maintaining st as branches and directories are consumed.
func (s *Service) reconcilePR(
	ctx context.Context, repo *schema.Repository, num int64, pr github.PR,
	st *reconcileState, progress progressFn, errs *[]error,
) int {
	if wt, ok := st.unlinked[pr.HeadBranch]; ok {
		s.linkWorktree(ctx, num, pr, wt, progress, errs)
		delete(st.unlinked, pr.HeadBranch)
		return 0
	}
	if dir := st.adoptable[pr.HeadBranch]; dir != "" && !st.usedDirs[dir] {
		if s.adoptWorktree(ctx, repo, num, pr, dir, progress, errs) {
			st.usedDirs[dir] = true
			return 1
		}
		return 0
	}
	if dir, ok := s.createWorktree(ctx, repo, num, pr, st.usedDirs, progress, errs); ok {
		st.usedDirs[dir] = true
		return 1
	}
	return 0
}

// linkWorktree associates an already-tracked worktree (matched by branch but
// carrying no PR number yet) with the open PR whose branch it holds. It mutates
// only the stored row — the directory is already on disk. Linking is
// reconciliation, not creation, so it is not counted toward created.
func (s *Service) linkWorktree(
	ctx context.Context, num int64, pr github.PR, wt schema.Worktree,
	progress progressFn, errs *[]error,
) {
	n := num
	if err := s.db.SetWorktreePR(ctx, wt.ID, &n); err != nil {
		*errs = append(*errs, fmt.Errorf("linking PR #%d to worktree %s: %w", num, wt.DirectoryPath, err))
		return
	}
	progress.send(WorktreeChange{Branch: pr.HeadBranch, PRNumber: &n, Action: ActionUpdated})
}

// adoptWorktree records an already-checked-out directory as a preexisting
// worktree for the PR, without touching git. It returns true when the row was
// stored.
func (s *Service) adoptWorktree(
	ctx context.Context, repo *schema.Repository, num int64, pr github.PR,
	dir string, progress progressFn, errs *[]error,
) bool {
	n := num
	row := &schema.Worktree{
		RepositoryID: repo.ID, GithubPRNumber: &n,
		BranchName: pr.HeadBranch, DirectoryPath: dir,
		CreatedBy: schema.CreatedByPreexisting,
	}
	if cerr := s.db.CreateWorktree(ctx, row); cerr != nil {
		*errs = append(*errs, fmt.Errorf("recording adopted worktree for PR #%d: %w", num, cerr))
		return false
	}
	progress.send(WorktreeChange{Branch: pr.HeadBranch, PRNumber: &n, Action: ActionAdopted})
	return true
}

// createWorktree adds a new worktree on disk for the PR and records it. It
// returns the chosen directory and true when both steps succeed.
func (s *Service) createWorktree(
	ctx context.Context, repo *schema.Repository, num int64, pr github.PR,
	usedDirs map[string]bool, progress progressFn, errs *[]error,
) (string, bool) {
	dir := s.resolveDir(repo, pr, usedDirs)
	if aerr := s.git.AddWorktree(ctx, repo.LocalPath, dir, repo.DefaultRemote, pr.HeadBranch); aerr != nil {
		*errs = append(*errs, fmt.Errorf("PR #%d (%s): %w", num, pr.HeadBranch, aerr))
		return "", false
	}
	n := num
	row := &schema.Worktree{
		RepositoryID: repo.ID, GithubPRNumber: &n,
		BranchName: pr.HeadBranch, DirectoryPath: dir,
		CreatedBy: schema.CreatedByLumberjack,
	}
	if cerr := s.db.CreateWorktree(ctx, row); cerr != nil {
		*errs = append(*errs, fmt.Errorf("recording worktree for PR #%d: %w", num, cerr))
		// Roll back the on-disk worktree so a retry can recreate it cleanly.
		_ = s.git.RemoveWorktree(ctx, repo.LocalPath, dir, true)
		return "", false
	}
	progress.send(WorktreeChange{Branch: pr.HeadBranch, PRNumber: &n, Action: ActionCheckedOut})
	return dir, true
}

// adoptableWorktrees returns the branch→directory map of worktrees git already
// has checked out but that Lumberjack is not yet tracking (their directory is
// not in usedDirs). These are directories a human created by hand, or ones a
// previous run left behind, which sync adopts rather than failing to recreate.
// A listing failure is recorded but non-fatal — sync falls back to creation.
func (s *Service) adoptableWorktrees(
	ctx context.Context, repo *schema.Repository, usedDirs map[string]bool, errs *[]error,
) map[string]string {
	refs, err := s.untrackedWorktrees(ctx, repo, usedDirs)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("listing existing worktrees: %w", err))
		return nil
	}
	byBranch := make(map[string]string, len(refs))
	for _, r := range refs {
		// First registered wins; git forbids the same branch in two worktrees, so
		// a branch maps to at most one directory in practice.
		if _, seen := byBranch[r.Branch]; !seen {
			byBranch[r.Branch] = r.Dir
		}
	}
	return byBranch
}

// untrackedWorktrees lists the worktrees git has registered for the repo that
// Lumberjack is not yet tracking: those whose directory is not in tracked,
// that are on a branch (not detached), and that are not the main checkout. Both
// sync (to adopt into open PRs) and init (to adopt at registration) build on it.
func (s *Service) untrackedWorktrees(
	ctx context.Context, repo *schema.Repository, tracked map[string]bool,
) ([]worktree.Ref, error) {
	refs, err := s.git.ListWorktrees(ctx, repo.LocalPath)
	if err != nil {
		return nil, err
	}
	out := refs[:0:0]
	for _, r := range refs {
		if r.Branch == "" || r.Dir == repo.LocalPath || tracked[r.Dir] {
			continue // detached HEAD, the main checkout, or already tracked
		}
		out = append(out, r)
	}
	return out, nil
}

// resolveDir computes the worktree directory for a PR, disambiguating a slug
// collision with an already-used directory by appending the PR number.
func (s *Service) resolveDir(repo *schema.Repository, pr github.PR, usedDirs map[string]bool) string {
	dir := worktree.Path(repo.WorktreeParentDir, repo.DirPrefix, pr.HeadBranch)
	if usedDirs[dir] {
		dir = fmt.Sprintf("%s-pr%d", dir, pr.Number)
	}
	return dir
}

// removeClosed removes worktrees whose PR is no longer open, retaining any
// that still need reconciliation (dirty or holding local-only commits) and
// never touching human-made (preexisting) worktrees. It returns the number
// removed.
func (s *Service) removeClosed(
	ctx context.Context, repo *schema.Repository, openByNum map[int64]github.PR,
	stored []schema.Worktree, progress progressFn, errs *[]error,
) (removed int) {
	for i := range stored {
		wt := stored[i]
		if s.prStillOpen(wt, openByNum) {
			continue // PR still open — keep the worktree
		}
		if wt.CreatedBy != schema.CreatedByLumberjack {
			continue // safety rail: never remove a human-made worktree
		}
		if s.removeOne(ctx, repo, wt, progress, errs) {
			removed++
		}
	}
	return removed
}

// prStillOpen reports whether the worktree's source PR is among the open set.
func (s *Service) prStillOpen(wt schema.Worktree, openByNum map[int64]github.PR) bool {
	if wt.GithubPRNumber == nil {
		return false
	}
	_, ok := openByNum[*wt.GithubPRNumber]
	return ok
}

// removeOne handles a single closed-PR worktree: prune it if its directory is
// gone, retain it if it still needs reconciliation, otherwise remove it. It
// returns true when the worktree was removed from tracking.
func (s *Service) removeOne(
	ctx context.Context, repo *schema.Repository, wt schema.Worktree,
	progress progressFn, errs *[]error,
) bool {
	st, rerr := worktree.Reconcile(ctx, s.git, wt.DirectoryPath, false)
	if rerr != nil {
		*errs = append(*errs, fmt.Errorf("reconciling %s: %w", wt.DirectoryPath, rerr))
		return false
	}
	if st.NeedsReconciliation {
		progress.send(WorktreeChange{
			Branch: wt.BranchName, PRNumber: wt.GithubPRNumber,
			Action: ActionRetained, Detail: st.Note,
		})
		return false
	}
	if !st.Missing {
		if rmErr := s.git.RemoveWorktree(ctx, repo.LocalPath, wt.DirectoryPath, false); rmErr != nil {
			*errs = append(*errs, fmt.Errorf("removing %s: %w", wt.DirectoryPath, rmErr))
			return false
		}
	}
	if derr := s.db.DeleteWorktree(ctx, wt.ID); derr != nil {
		*errs = append(*errs, derr)
		return false
	}
	progress.send(WorktreeChange{
		Branch: wt.BranchName, PRNumber: wt.GithubPRNumber,
		Action: ActionDeleted, Detail: "PR closed",
	})
	return true
}
