package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func (c *Client) MarkViewed(ctx context.Context, repoID int64, a schema.Authorship, seenThrough time.Time) error {
	return c.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		view := &schema.ListView{RepositoryID: repoID, Authorship: a}
		err := tx.NewSelect().Model(view).WherePK().Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("reading list view: %w", err)
		case !seenThrough.After(view.ViewedAt):
			return nil
		}
		view.ViewedAt = seenThrough
		if _, err := tx.NewInsert().Model(view).
			On("CONFLICT (repository_id, authorship) DO UPDATE").
			Set("viewed_at = EXCLUDED.viewed_at").
			Exec(ctx); err != nil {
			return fmt.Errorf("recording list view: %w", err)
		}
		return nil
	})
}

func (c *Client) ListViews(ctx context.Context, repoID int64) (map[schema.Authorship]time.Time, error) {
	var views []schema.ListView
	if err := c.NewSelect().Model(&views).Where("repository_id = ?", repoID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing list views: %w", err)
	}
	out := make(map[schema.Authorship]time.Time, len(views))
	for _, v := range views {
		out[v.Authorship] = v.ViewedAt
	}
	return out, nil
}
