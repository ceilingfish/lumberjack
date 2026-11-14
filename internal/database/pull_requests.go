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
	Author string
}

// ReplaceOpenPRs makes the pull_requests table reflect exactly the currently
// open PRs for a repository: it upserts each (keyed on repository_id +
// github_pr_number) and soft-deletes rows for PRs no longer open. Mutable PR detail
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
				AuthorLogin:    pr.Author,
			}
			if _, err := tx.NewInsert().Model(row).
				On("CONFLICT (repository_id, github_pr_number) DO UPDATE").
				Set("branch_name = EXCLUDED.branch_name").
				Set("last_synced_at = EXCLUDED.last_synced_at").
				Set("author_login = EXCLUDED.author_login").
				Set("deleted_at = NULL").
				Exec(ctx); err != nil {
				return fmt.Errorf("upserting pull request #%d: %w", pr.Number, err)
			}
		}

		closed := tx.NewUpdate().Model((*schema.PullRequest)(nil)).
			Set("deleted_at = ?", at).
			Where("repository_id = ?", repoID).
			Where("deleted_at IS NULL")
		if len(keep) > 0 {
			closed = closed.Where("github_pr_number NOT IN (?)", bun.List(keep))
		}
		if _, err := closed.Exec(ctx); err != nil {
			return fmt.Errorf("soft-deleting closed pull requests: %w", err)
		}
		return nil
	})
}

func (c *Client) PRAuthors(ctx context.Context, repoID int64) (map[int64]string, error) {
	var prs []schema.PullRequest
	if err := c.NewSelect().Model(&prs).
		Column("github_pr_number", "author_login").
		Where("repository_id = ?", repoID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing pull request authors: %w", err)
	}
	out := make(map[int64]string, len(prs))
	for _, pr := range prs {
		out[pr.GithubPRNumber] = pr.AuthorLogin
	}
	return out, nil
}

func (c *Client) RecordClosedPR(ctx context.Context, repoID int64, pr TrackedPR, at time.Time) error {
	row := &schema.PullRequest{
		RepositoryID:   repoID,
		GithubPRNumber: pr.Number,
		BranchName:     pr.Branch,
		LastSyncedAt:   &at,
		AuthorLogin:    pr.Author,
		DeletedAt:      &at,
	}
	if _, err := c.NewInsert().Model(row).
		On("CONFLICT (repository_id, github_pr_number) DO UPDATE").
		Set("author_login = EXCLUDED.author_login").
		Exec(ctx); err != nil {
		return fmt.Errorf("recording pull request #%d: %w", pr.Number, err)
	}
	return nil
}
