-- +goose Up
ALTER TABLE worktrees ADD COLUMN origin TEXT NOT NULL DEFAULT 'adopted'
CHECK (origin IN ('local', 'synced', 'adopted'));

ALTER TABLE worktrees ADD COLUMN authorship_override TEXT
CHECK (authorship_override IN ('mine', 'theirs'));

ALTER TABLE pull_requests ADD COLUMN author_login TEXT NOT NULL DEFAULT '';

ALTER TABLE pull_requests ADD COLUMN deleted_at TIMESTAMP;

-- +goose Down
DELETE FROM pull_requests
WHERE deleted_at IS NOT NULL;

ALTER TABLE pull_requests DROP COLUMN deleted_at;

ALTER TABLE pull_requests DROP COLUMN author_login;

ALTER TABLE worktrees DROP COLUMN authorship_override;

ALTER TABLE worktrees DROP COLUMN origin;
