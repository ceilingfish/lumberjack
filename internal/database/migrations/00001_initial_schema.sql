-- +goose Up
CREATE TABLE repositories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    local_path TEXT NOT NULL,
    worktree_parent_dir TEXT NOT NULL,
    dir_prefix TEXT NOT NULL,
    github_owner TEXT NOT NULL,
    github_name TEXT NOT NULL,
    default_remote TEXT NOT NULL,
    host TEXT NOT NULL,
    last_synced_at TIMESTAMP,
    last_sync_status TEXT,
    last_sync_error TEXT,
    etag_pulls TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_repositories_local_path ON repositories (local_path);

CREATE TABLE pull_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    github_pr_number INTEGER NOT NULL,
    branch_name TEXT NOT NULL,
    last_synced_at TIMESTAMP,
    UNIQUE (repository_id, github_pr_number)
);

CREATE INDEX idx_pull_requests_repository_id ON pull_requests (repository_id);

CREATE TABLE worktrees (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    github_pr_number INTEGER,
    branch_name TEXT NOT NULL,
    directory_path TEXT NOT NULL,
    created_by TEXT NOT NULL,
    last_synced_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (repository_id, directory_path)
);

CREATE INDEX idx_worktrees_repository_id ON worktrees (repository_id);

CREATE TABLE sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP,
    status TEXT NOT NULL,
    worktrees_created INTEGER NOT NULL DEFAULT 0,
    worktrees_removed INTEGER NOT NULL DEFAULT 0,
    error TEXT
);

CREATE INDEX idx_sync_runs_repository_id ON sync_runs (repository_id);

-- +goose Down
DROP TABLE sync_runs;
DROP TABLE worktrees;
DROP TABLE pull_requests;
DROP TABLE repositories;
