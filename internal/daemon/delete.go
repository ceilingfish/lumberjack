package daemon

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// DeleteResult reports the outcome of a worktree deletion request, mirroring
// the DeleteWorktreeResponse message.
type DeleteResult struct {
	Deleted              bool
	RequiresConfirmation bool
	CommitsAtRisk        int64
	Message              string
}

// DeleteWorktree removes the worktree identified by ref within repo. When the
// worktree holds work that would be lost (uncommitted changes or local-only
// commits), or its checked-out branch is not its PR's, and force is false, it
// returns RequiresConfirmation=true without deleting, so the CLI can warn the
// user; a second call with force=true then performs the deletion (see
// DeleteWorktreeRequest.force in the proto).
func (s *Service) DeleteWorktree(ctx context.Context, repo *schema.Repository, ref string, force bool) (DeleteResult, error) {
	defer s.lockRepository(repo.ID)()

	var res DeleteResult
	err := s.withRepoLogin(ctx, repo, func(ctx context.Context) error {
		var derr error
		res, derr = s.deleteWorktreeLocked(ctx, repo, ref, force)
		return derr
	})
	return res, err
}

// deleteWorktreeLocked is the body of DeleteWorktree, run under s.mu with gh's
// active account already switched to the repository's login (the git fetch it
// performs authenticates through gh's active account).
func (s *Service) deleteWorktreeLocked(ctx context.Context, repo *schema.Repository, ref string, force bool) (DeleteResult, error) {
	wt, err := s.db.FindWorktree(ctx, repo.ID, ref)
	if err != nil {
		return DeleteResult{}, err
	}

	// Fetch first so the local-only commit count reflects the current remote.
	if err := s.git.Fetch(ctx, repo.LocalPath, repo.DefaultRemote); err != nil {
		return DeleteResult{}, fmt.Errorf("fetching %s: %w", repo.DefaultRemote, err)
	}
	// A merged PR's commits are on the base branch, so they must not be counted
	// as commits-at-risk when warning before deletion.
	state := worktree.PRGone
	if wt.GithubPRNumber != nil {
		merged, merr := s.gh.PRMerged(ctx, repoInfo(repo), *wt.GithubPRNumber)
		if merr != nil {
			return DeleteResult{}, fmt.Errorf("checking PR #%d state: %w", *wt.GithubPRNumber, merr)
		}
		if merged {
			state = worktree.PRMerged
		}
	}
	st, err := worktree.Reconcile(ctx, s.git, wt.DirectoryPath, prBranchOf(*wt), state)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("reconciling %s: %w", wt.DirectoryPath, err)
	}

	if !force && !st.Missing && st.NeedsReconciliation {
		return DeleteResult{
			RequiresConfirmation: true,
			CommitsAtRisk:        st.LocalOnlyCommits,
			Message:              confirmMessage(st),
		}, nil
	}

	if !st.Missing {
		if err := s.git.RemoveWorktree(ctx, repo.LocalPath, wt.DirectoryPath, force); err != nil {
			return DeleteResult{}, fmt.Errorf("removing %s: %w", wt.DirectoryPath, err)
		}
	}
	if err := s.db.DeleteWorktree(ctx, wt.ID); err != nil {
		return DeleteResult{}, err
	}
	s.events.Publish(Event{
		Type: EventWorktreeChanged, Repository: repo,
		Change: &WorktreeChange{
			Branch: wt.BranchName, PRNumber: wt.GithubPRNumber,
			Action: ActionDeleted, Detail: "deleted by request",
		},
	})
	return DeleteResult{Deleted: true, Message: fmt.Sprintf("deleted %s", filepath.Base(wt.DirectoryPath))}, nil
}

// confirmMessage renders the warning shown before a forced delete.
func confirmMessage(st worktree.Status) string {
	var atRisk string
	switch {
	case st.Dirty && st.LocalOnlyCommits > 0:
		atRisk = fmt.Sprintf("worktree has uncommitted changes and %d local-only commit(s) that will be lost", st.LocalOnlyCommits)
	case st.Dirty:
		atRisk = "worktree has uncommitted changes that will be lost"
	case st.LocalOnlyCommits > 0:
		atRisk = fmt.Sprintf("worktree has %d local-only commit(s) that will be lost", st.LocalOnlyCommits)
	}
	if !st.BranchDisparity {
		return atRisk
	}
	disparity := fmt.Sprintf("is checked out on %s rather than its PR branch", st.CheckedOutBranch)
	if atRisk == "" {
		return "worktree " + disparity
	}
	return atRisk + ", and " + disparity
}
