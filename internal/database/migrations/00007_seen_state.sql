-- +goose Up
ALTER TABLE pull_requests ADD COLUMN first_seen_at TIMESTAMP;

UPDATE pull_requests SET first_seen_at = COALESCE(last_synced_at, CURRENT_TIMESTAMP);

ALTER TABLE pull_requests ADD COLUMN conflicted_since TIMESTAMP;

ALTER TABLE pull_requests ADD COLUMN checks_failing_since TIMESTAMP;

CREATE TABLE list_views (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    authorship TEXT NOT NULL CHECK (authorship IN ('mine', 'theirs')),
    viewed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (repository_id, authorship)
);

-- +goose Down
DROP TABLE list_views;

ALTER TABLE pull_requests DROP COLUMN checks_failing_since;

ALTER TABLE pull_requests DROP COLUMN conflicted_since;

ALTER TABLE pull_requests DROP COLUMN first_seen_at;
