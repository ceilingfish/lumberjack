package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// AddResult reports the outcome of an on-demand worktree creation, mirroring
// the AddWorktreeResponse message.
type AddResult struct {
	DirectoryPath string
	Branch        string
	// BranchCreated is true when the branch did not exist on the remote or
	// locally and was created off the default branch for this worktree.
	BranchCreated bool
	// SetupError is the setup step that failed, if any. The worktree is created
	// and tracked regardless — setup failures are surfaced, not fatal (see
	// runSetupSteps).
	SetupError string
}

// AddWorktree creates a worktree for branch in repo's conventional location
// (the same parentDir/<prefix>-<slug> path sync uses) and records it as
// tracked, with no PR attached — sync links one later if a PR opens for the
// branch. It then runs the repository's setup steps against the new worktree.
//
// The branch need not exist yet: an existing remote branch is checked out
// tracking it, an existing local branch is checked out as-is, and otherwise a
// new branch is created off the default branch.
func (s *Service) AddWorktree(ctx context.Context, repo *schema.Repository, branch string) (AddResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res AddResult
	err := s.withRepoLogin(ctx, repo, func() error {
		var aerr error
		res, aerr = s.addWorktreeLocked(ctx, repo, branch)
		return aerr
	})
	return res, err
}

// addWorktreeLocked is the body of AddWorktree, run under s.mu with gh's active
// account already switched to the repository's login (the git fetch and the
// worktree creation authenticate through it).
func (s *Service) addWorktreeLocked(ctx context.Context, repo *schema.Repository, branch string) (AddResult, error) {
	if existing, err := s.db.FindWorktree(ctx, repo.ID, branch); err == nil {
		return AddResult{}, fmt.Errorf("%s is already checked out at %s", branch, existing.DirectoryPath)
	} else if !errors.Is(err, database.ErrWorktreeNotFound) {
		return AddResult{}, err
	}

	dir := worktree.Path(repo.WorktreeParentDir, repo.DirPrefix, branch)
	if _, err := os.Stat(dir); err == nil {
		return AddResult{}, fmt.Errorf("%s already exists", dir)
	}

	// Fetch first so an existing remote branch is visible to check out, and the
	// default branch we may branch from is current.
	if err := s.git.Fetch(ctx, repo.LocalPath, repo.DefaultRemote); err != nil {
		return AddResult{}, fmt.Errorf("fetching %s: %w", repo.DefaultRemote, err)
	}

	created, err := s.addWorktreeDir(ctx, repo, dir, branch)
	if err != nil {
		return AddResult{}, err
	}

	row := &schema.Worktree{RepositoryID: repo.ID, BranchName: branch, DirectoryPath: dir}
	if err := s.db.CreateWorktree(ctx, row); err != nil {
		// Roll back the on-disk worktree so a retry can recreate it cleanly.
		_ = s.git.RemoveWorktree(ctx, repo.LocalPath, dir, true)
		return AddResult{}, fmt.Errorf("recording worktree for %s: %w", branch, err)
	}

	setupErr := s.runSetupSteps(ctx, repo, dir, row.ID, false /* preserveExisting */)
	s.events.Publish(Event{
		Type: EventWorktreeChanged, Repository: repo,
		Change: &WorktreeChange{
			Branch: branch, Action: ActionCheckedOut,
			DirectoryPath: dir, Detail: "added by request",
		},
	})
	return AddResult{DirectoryPath: dir, Branch: branch, BranchCreated: created, SetupError: setupErr}, nil
}

// addWorktreeDir creates the worktree on disk, reporting whether the branch had
// to be created. It first tries the same path sync takes (track the remote
// branch, or check out an existing local one) and falls back to branching off
// the default branch for a branch that exists nowhere yet.
//
// The fallback is a probe, not a diagnosis: git's failure for a branch that
// does not exist is indistinguishable here from one caused by a locked index,
// an unreadable parent, or a branch already checked out elsewhere. So when the
// fallback also fails, both errors are reported — the first attempt's is
// usually the one naming the real cause.
func (s *Service) addWorktreeDir(ctx context.Context, repo *schema.Repository, dir, branch string) (bool, error) {
	addErr := s.git.AddWorktree(ctx, repo.LocalPath, dir, repo.DefaultRemote, branch)
	if addErr == nil {
		return false, nil
	}
	base, err := s.git.DefaultBranch(ctx, repo.LocalPath, repo.DefaultRemote)
	if err != nil {
		return false, errors.Join(
			fmt.Errorf("checking out %s: %w", branch, addErr),
			fmt.Errorf("determining default branch to branch %s from: %w", branch, err),
		)
	}
	if err := s.git.AddWorktreeNewBranch(ctx, repo.LocalPath, dir, repo.DefaultRemote+"/"+base, branch); err != nil {
		return false, errors.Join(
			fmt.Errorf("checking out %s: %w", branch, addErr),
			fmt.Errorf("creating %s off %s instead: %w", branch, base, err),
		)
	}
	return true, nil
}
