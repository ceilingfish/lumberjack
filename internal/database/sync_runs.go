package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// StartSyncRun opens an audit-log entry for a reconciliation, setting run.ID.
// The daemon finishes it with FinishSyncRun.
func (c *Client) StartSyncRun(ctx context.Context, repoID int64, at time.Time) (*schema.SyncRun, error) {
	run := &schema.SyncRun{RepositoryID: repoID, StartedAt: at, Status: schema.SyncStatusOK}
	if _, err := c.NewInsert().Model(run).Exec(ctx); err != nil {
		return nil, fmt.Errorf("starting sync run: %w", err)
	}
	return run, nil
}

// FinishSyncRun records the outcome of a sync run: counts, status, finish
// time, and optional error message.
func (c *Client) FinishSyncRun(ctx context.Context, run *schema.SyncRun, at time.Time, created, removed int, syncErr error) error {
	status := schema.SyncStatusOK
	var errMsg *string
	if syncErr != nil {
		status = schema.SyncStatusError
		m := syncErr.Error()
		errMsg = &m
	}
	if _, err := c.NewUpdate().Model((*schema.SyncRun)(nil)).
		Set("finished_at = ?", at).
		Set("status = ?", status).
		Set("worktrees_created = ?", created).
		Set("worktrees_removed = ?", removed).
		Set("error = ?", errMsg).
		Where("id = ?", run.ID).Exec(ctx); err != nil {
		return fmt.Errorf("finishing sync run: %w", err)
	}
	return nil
}

// LatestSyncRun returns the most recent sync run for a repository, or nil if
// the repository has never been synced.
func (c *Client) LatestSyncRun(ctx context.Context, repoID int64) (*schema.SyncRun, error) {
	run := new(schema.SyncRun)
	err := c.NewSelect().Model(run).
		Where("repository_id = ?", repoID).
		Order("started_at DESC").Limit(1).Scan(ctx)
	if err != nil {
		// No rows is not an error here — a never-synced repo simply has none.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("loading latest sync run: %w", err)
	}
	return run, nil
}
