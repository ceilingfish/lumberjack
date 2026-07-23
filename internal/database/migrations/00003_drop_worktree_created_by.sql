-- +goose Up
-- created_by flagged whether the daemon or a human created a worktree, so the
-- daemon would never auto-remove human-made ones. Lumberjack now manages every
-- tracked worktree uniformly (removing only provably-safe ones), so the column
-- is obsolete.
ALTER TABLE worktrees DROP COLUMN created_by;

-- +goose Down
ALTER TABLE worktrees ADD COLUMN created_by TEXT NOT NULL DEFAULT 'preexisting';
