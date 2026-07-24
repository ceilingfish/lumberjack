package database

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// CreateWorktree inserts a worktree row, setting wt.ID.
func (c *Client) CreateWorktree(ctx context.Context, wt *schema.Worktree) error {
	if _, err := c.NewInsert().Model(wt).Exec(ctx); err != nil {
		return fmt.Errorf("inserting worktree: %w", err)
	}
	return nil
}

// ListWorktrees returns a repository's worktrees, oldest first.
func (c *Client) ListWorktrees(ctx context.Context, repoID int64) ([]schema.Worktree, error) {
	var wts []schema.Worktree
	if err := c.NewSelect().Model(&wts).
		Where("repository_id = ?", repoID).Order("id ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	return wts, nil
}

// FindWorktree resolves a worktree within a repository by reference, which may
// be its branch name, its full directory path, or the directory's base name.
// It returns ErrWorktreeNotFound when nothing matches.
func (c *Client) FindWorktree(ctx context.Context, repoID int64, ref string) (*schema.Worktree, error) {
	wts, err := c.ListWorktrees(ctx, repoID)
	if err != nil {
		return nil, err
	}
	for i := range wts {
		wt := &wts[i]
		if wt.BranchName == ref || wt.DirectoryPath == ref || filepath.Base(wt.DirectoryPath) == ref {
			return wt, nil
		}
	}
	return nil, ErrWorktreeNotFound
}

// SetWorktreePR links a worktree row to a PR number (or clears it when nil).
// It lets sync associate an adopted worktree — tracked by branch but with no PR
// yet — to the open PR whose branch it holds, without recreating anything.
func (c *Client) SetWorktreePR(ctx context.Context, id int64, prNumber *int64) error {
	if _, err := c.NewUpdate().Model((*schema.Worktree)(nil)).
		Set("github_pr_number = ?", prNumber).
		Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("updating worktree PR: %w", err)
	}
	return nil
}

// TouchWorktreesSyncedAt stamps last_synced_at to at on every worktree row
// belonging to a repository. It is called once per successful sync, after
// worktrees are created/adopted/linked/removed, so every worktree still
// tracked for the repository reflects the sync time — mirroring how
// UpdateSyncResult stamps the repository row itself.
func (c *Client) TouchWorktreesSyncedAt(ctx context.Context, repoID int64, at time.Time) error {
	if _, err := c.NewUpdate().Model((*schema.Worktree)(nil)).
		Set("last_synced_at = ?", at).
		Where("repository_id = ?", repoID).Exec(ctx); err != nil {
		return fmt.Errorf("touching worktree sync time: %w", err)
	}
	return nil
}

// SetWorktreeSetupError records (or clears, with nil) the setup step that
// failed when this worktree was cloned, surfaced alongside its live
// reconciliation status.
func (c *Client) SetWorktreeSetupError(ctx context.Context, id int64, msg *string) error {
	if _, err := c.NewUpdate().Model((*schema.Worktree)(nil)).
		Set("setup_error = ?", msg).
		Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("updating worktree setup error: %w", err)
	}
	return nil
}

// DeleteWorktree removes a worktree row by primary key.
func (c *Client) DeleteWorktree(ctx context.Context, id int64) error {
	if _, err := c.NewDelete().Model((*schema.Worktree)(nil)).
		Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("deleting worktree: %w", err)
	}
	return nil
}
