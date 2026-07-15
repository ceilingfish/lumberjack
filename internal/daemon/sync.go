package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	Fetch(ctx context.Context, repoPath, remote string) error
	AddWorktree(ctx context.Context, repoPath, dir, remote, branch string) error
	RemoveWorktree(ctx context.Context, repoPath, dir string, force bool) error
}

// GHOps is the gh surface the sync engine needs.
type GHOps interface {
	RepoInfo(ctx context.Context, dir string) (github.RepoInfo, error)
	ListOpenPRs(ctx context.Context, repo github.RepoInfo) ([]github.PR, error)
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

// progressFn receives human-readable progress lines during a sync. It may be
// nil.
type progressFn func(msg string)

func (p progressFn) emit(format string, args ...any) {
	if p != nil {
		p(fmt.Sprintf(format, args...))
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
	openByNum, err := s.fetchOpenPRs(ctx, repo)
	if err != nil {
		return nil, err
	}
	stored, err := s.db.ListWorktrees(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	views := make([]WorktreeView, 0, len(stored))
	for i := range stored {
		wt := stored[i]
		open := wt.GithubPRNumber != nil
		if open {
			_, open = openByNum[*wt.GithubPRNumber]
		}
		st, err := worktree.Reconcile(ctx, s.git, wt.DirectoryPath, open)
		if err != nil {
			return nil, fmt.Errorf("reconciling %s: %w", wt.DirectoryPath, err)
		}
		views = append(views, WorktreeView{Worktree: wt, Status: st, PROpen: open})
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
		if uErr := s.db.UpdateSyncResult(ctx, repo.ID, finish, err, ""); uErr != nil && err == nil {
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

// createMissing creates a worktree for each open PR that has no tracked
// worktree yet, returning the number created.
func (s *Service) createMissing(
	ctx context.Context, repo *schema.Repository, openByNum map[int64]github.PR,
	stored []schema.Worktree, progress progressFn, errs *[]error,
) (created int) {
	havePR := make(map[int64]bool, len(stored))
	usedDirs := make(map[string]bool, len(stored))
	for _, wt := range stored {
		if wt.GithubPRNumber != nil {
			havePR[*wt.GithubPRNumber] = true
		}
		usedDirs[wt.DirectoryPath] = true
	}

	for num, pr := range openByNum {
		if havePR[num] {
			continue
		}
		dir := s.resolveDir(repo, pr, usedDirs)
		progress.emit("creating worktree for PR #%d (%s)", num, pr.HeadBranch)
		if aerr := s.git.AddWorktree(ctx, repo.LocalPath, dir, repo.DefaultRemote, pr.HeadBranch); aerr != nil {
			*errs = append(*errs, fmt.Errorf("PR #%d (%s): %w", num, pr.HeadBranch, aerr))
			continue
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
			continue
		}
		usedDirs[dir] = true
		created++
	}
	return created
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
		progress.emit("retaining %s: %s", baseName(wt.DirectoryPath), st.Note)
		return false
	}
	if !st.Missing {
		progress.emit("removing %s (PR closed)", baseName(wt.DirectoryPath))
		if rmErr := s.git.RemoveWorktree(ctx, repo.LocalPath, wt.DirectoryPath, false); rmErr != nil {
			*errs = append(*errs, fmt.Errorf("removing %s: %w", wt.DirectoryPath, rmErr))
			return false
		}
	}
	if derr := s.db.DeleteWorktree(ctx, wt.ID); derr != nil {
		*errs = append(*errs, derr)
		return false
	}
	return true
}

// baseName is filepath.Base, aliased so progress lines stay readable.
func baseName(p string) string { return filepath.Base(p) }
