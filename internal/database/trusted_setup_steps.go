package database

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func (c *Client) TrustSetupSteps(ctx context.Context, repoID int64, checksum string) error {
	row := &schema.TrustedSetupStep{RepositoryID: repoID, Checksum: checksum}
	if _, err := c.NewInsert().Model(row).
		On("CONFLICT (repository_id, checksum) DO NOTHING").
		Exec(ctx); err != nil {
		return fmt.Errorf("trusting setup steps: %w", err)
	}
	return nil
}

func (c *Client) TrustedSetupChecksums(ctx context.Context, repoID int64) ([]string, error) {
	var rows []schema.TrustedSetupStep
	if err := c.NewSelect().Model(&rows).
		Where("repository_id = ?", repoID).
		Order("trusted_at DESC", "id DESC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing trusted setup steps: %w", err)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Checksum
	}
	return out, nil
}
