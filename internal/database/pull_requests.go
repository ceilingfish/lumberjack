package database

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// TrackedPR is the branch mapping the daemon records for an open PR.
type TrackedPR struct {
	Number int64
	Branch string
}

// ReplaceOpenPRs makes the pull_requests table reflect exactly the currently
// open PRs for a repository: it upserts each (keyed on repository_id +
// github_pr_number) and removes rows for PRs no longer open. Mutable PR detail
// is never stored — only the branch mapping and last-seen time (docs/schema.md).
func (c *Client) ReplaceOpenPRs(ctx context.Context, repoID int64, prs []TrackedPR, at time.Time) error {
	return c.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		keep := make([]int64, 0, len(prs))
		for _, pr := range prs {
			keep = append(keep, pr.Number)
			row := &schema.PullRequest{
				RepositoryID:   repoID,
				GithubPRNumber: pr.Number,
				BranchName:     pr.Branch,
				LastSyncedAt:   &at,
			}
			if _, err := tx.NewInsert().Model(row).
				On("CONFLICT (repository_id, github_pr_number) DO UPDATE").
				Set("branch_name = EXCLUDED.branch_name").
				Set("last_synced_at = EXCLUDED.last_synced_at").
				Exec(ctx); err != nil {
				return fmt.Errorf("upserting pull request #%d: %w", pr.Number, err)
			}
		}

		del := tx.NewDelete().Model((*schema.PullRequest)(nil)).
			Where("repository_id = ?", repoID)
		if len(keep) > 0 {
			del = del.Where("github_pr_number NOT IN (?)", bun.List(keep))
		}
		if _, err := del.Exec(ctx); err != nil {
			return fmt.Errorf("pruning closed pull requests: %w", err)
		}
		return nil
	})
}

// ListPRs returns the tracked open PRs for a repository.
func (c *Client) ListPRs(ctx context.Context, repoID int64) ([]schema.PullRequest, error) {
	var prs []schema.PullRequest
	if err := c.NewSelect().Model(&prs).
		Where("repository_id = ?", repoID).Order("github_pr_number ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing pull requests: %w", err)
	}
	return prs, nil
}
