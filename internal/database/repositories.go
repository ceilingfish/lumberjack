package database

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// CreateRepository inserts a new tracked repository, setting repo.ID. It
// returns ErrRepositoryExists if the local path is already tracked (the
// daemon surfaces this as AlreadyExists).
func (c *Client) CreateRepository(ctx context.Context, repo *schema.Repository) error {
	exists, err := c.NewSelect().Model((*schema.Repository)(nil)).
		Where("local_path = ?", repo.LocalPath).Exists(ctx)
	if err != nil {
		return fmt.Errorf("checking existing repository: %w", err)
	}
	if exists {
		return ErrRepositoryExists
	}
	if _, err := c.NewInsert().Model(repo).Exec(ctx); err != nil {
		return fmt.Errorf("inserting repository: %w", err)
	}
	return nil
}

// ListRepositories returns every tracked repository, oldest first.
func (c *Client) ListRepositories(ctx context.Context) ([]schema.Repository, error) {
	var repos []schema.Repository
	if err := c.NewSelect().Model(&repos).Order("id ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}
	return repos, nil
}

// FindRepository resolves a repository by reference, which may be its local
// path, its dir_prefix, or its GitHub name. An exact local-path match wins
// over a name match. It returns ErrRepositoryNotFound when nothing matches.
func (c *Client) FindRepository(ctx context.Context, ref string) (*schema.Repository, error) {
	var repos []schema.Repository
	err := c.NewSelect().Model(&repos).
		Where("local_path = ? OR dir_prefix = ? OR github_name = ?", ref, ref, ref).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding repository %q: %w", ref, err)
	}
	if len(repos) == 0 {
		return nil, ErrRepositoryNotFound
	}
	// Prefer an exact local-path match, then dir_prefix, then fall through.
	for i := range repos {
		if repos[i].LocalPath == ref {
			return &repos[i], nil
		}
	}
	for i := range repos {
		if repos[i].DirPrefix == ref {
			return &repos[i], nil
		}
	}
	return &repos[0], nil
}

// DeleteRepository stops tracking the repository with the given primary key,
// removing its worktree (and, via ON DELETE CASCADE, pull_request and sync_run)
// rows in the same transaction. It touches only the database — nothing on disk
// or on GitHub — and returns the number of worktree rows removed. It returns
// ErrRepositoryNotFound when no repository has that id.
func (c *Client) DeleteRepository(ctx context.Context, repoID int64) (worktreesRemoved int64, err error) {
	err = c.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, derr := tx.NewDelete().Model((*schema.Worktree)(nil)).
			Where("repository_id = ?", repoID).Exec(ctx)
		if derr != nil {
			return fmt.Errorf("deleting worktrees: %w", derr)
		}
		worktreesRemoved, derr = res.RowsAffected()
		if derr != nil {
			return fmt.Errorf("counting deleted worktrees: %w", derr)
		}

		res, derr = tx.NewDelete().Model((*schema.Repository)(nil)).
			Where("id = ?", repoID).Exec(ctx)
		if derr != nil {
			return fmt.Errorf("deleting repository: %w", derr)
		}
		affected, derr := res.RowsAffected()
		if derr != nil {
			return fmt.Errorf("counting deleted repository: %w", derr)
		}
		if affected == 0 {
			return ErrRepositoryNotFound
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return worktreesRemoved, nil
}

// UpdateSyncResult records the outcome of a sync on the repository row. A nil
// syncErr records success; a non-nil one records the error message.
func (c *Client) UpdateSyncResult(ctx context.Context, repoID int64, at time.Time, syncErr error) error {
	status := schema.SyncStatusOK
	var errMsg *string
	if syncErr != nil {
		status = schema.SyncStatusError
		m := syncErr.Error()
		errMsg = &m
	}
	if _, err := c.NewUpdate().Model((*schema.Repository)(nil)).
		Set("last_synced_at = ?", at).
		Set("last_sync_status = ?", status).
		Set("last_sync_error = ?", errMsg).
		Where("id = ?", repoID).Exec(ctx); err != nil {
		return fmt.Errorf("updating sync result: %w", err)
	}
	return nil
}

// UpdateLogin sets the gh account a repository operates under. An empty login
// is allowed and clears it (reverting the repo to "no account switching").
func (c *Client) UpdateLogin(ctx context.Context, repoID int64, login string) error {
	if _, err := c.NewUpdate().Model((*schema.Repository)(nil)).
		Set("login = ?", login).
		Where("id = ?", repoID).Exec(ctx); err != nil {
		return fmt.Errorf("updating repository login: %w", err)
	}
	return nil
}
