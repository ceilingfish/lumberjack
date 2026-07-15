package daemon

import (
	"context"
	"fmt"

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
// commits) and force is false, it returns RequiresConfirmation=true without
// deleting, so the CLI can warn the user; a second call with force=true then
// performs the deletion (see DeleteWorktreeRequest.force in the proto).
func (s *Service) DeleteWorktree(ctx context.Context, repo *schema.Repository, ref string, force bool) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wt, err := s.db.FindWorktree(ctx, repo.ID, ref)
	if err != nil {
		return DeleteResult{}, err
	}

	// Fetch first so the local-only commit count reflects the current remote.
	if err := s.git.Fetch(ctx, repo.LocalPath, repo.DefaultRemote); err != nil {
		return DeleteResult{}, fmt.Errorf("fetching %s: %w", repo.DefaultRemote, err)
	}
	st, err := worktree.Reconcile(ctx, s.git, wt.DirectoryPath, false)
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
	return DeleteResult{Deleted: true, Message: fmt.Sprintf("deleted %s", baseName(wt.DirectoryPath))}, nil
}

// confirmMessage renders the warning shown before a forced delete.
func confirmMessage(st worktree.Status) string {
	switch {
	case st.Dirty && st.LocalOnlyCommits > 0:
		return fmt.Sprintf("worktree has uncommitted changes and %d local-only commit(s) that will be lost", st.LocalOnlyCommits)
	case st.Dirty:
		return "worktree has uncommitted changes that will be lost"
	default:
		return fmt.Sprintf("worktree has %d local-only commit(s) that will be lost", st.LocalOnlyCommits)
	}
}
