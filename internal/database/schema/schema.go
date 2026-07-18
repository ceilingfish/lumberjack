// Package schema defines the Bun models for Lumberjack's tracked state.
//
// These structs mirror the tables created by the goose migrations under
// internal/database/migrations. Only state that cannot be recovered from git
// or the gh CLI is persisted here; live PR and worktree status is fetched
// fresh when needed rather than cached (see docs/schema.md).
package schema

import (
	"time"

	"github.com/uptrace/bun"
)

// Repository is one tracked repo: the identity Lumberjack syncs against.
type Repository struct {
	bun.BaseModel `bun:"table:repositories,alias:r"`

	ID                int64  `bun:"id,pk,autoincrement"`
	LocalPath         string `bun:"local_path,notnull"`
	WorktreeParentDir string `bun:"worktree_parent_dir,notnull"`
	DirPrefix         string `bun:"dir_prefix,notnull"`
	GithubOwner       string `bun:"github_owner,notnull"`
	GithubName        string `bun:"github_name,notnull"`
	DefaultRemote     string `bun:"default_remote,notnull"`
	Host              string `bun:"host,notnull"`
	// Login is the gh account this repo was registered under. The daemon
	// switches to it before any git/gh operation and restores the prior account
	// afterwards. Empty for repos tracked before login capture — no switching.
	Login          string     `bun:"login,notnull"`
	LastSyncedAt   *time.Time `bun:"last_synced_at"`
	LastSyncStatus *string    `bun:"last_sync_status"`
	LastSyncError  *string    `bun:"last_sync_error"`
	EtagPulls      *string    `bun:"etag_pulls"`
	CreatedAt      time.Time  `bun:"created_at,notnull,default:current_timestamp"`
}

// PullRequest is a PR the daemon is tracking, mapped to its head branch.
// Everything mutable about a PR (title, state, head SHA, ...) is fetched live
// from gh; only the branch mapping and last-seen time are stored.
type PullRequest struct {
	bun.BaseModel `bun:"table:pull_requests,alias:pr"`

	ID             int64      `bun:"id,pk,autoincrement"`
	RepositoryID   int64      `bun:"repository_id,notnull"`
	GithubPRNumber int64      `bun:"github_pr_number,notnull"`
	BranchName     string     `bun:"branch_name,notnull"`
	LastSyncedAt   *time.Time `bun:"last_synced_at"`
}

// Worktree is the local branch ↔ directory mapping. Stored, never recomputed,
// so slug-rule changes can't orphan an existing worktree.
type Worktree struct {
	bun.BaseModel `bun:"table:worktrees,alias:w"`

	ID             int64      `bun:"id,pk,autoincrement"`
	RepositoryID   int64      `bun:"repository_id,notnull"`
	GithubPRNumber *int64     `bun:"github_pr_number"`
	BranchName     string     `bun:"branch_name,notnull"`
	DirectoryPath  string     `bun:"directory_path,notnull"`
	CreatedBy      string     `bun:"created_by,notnull"`
	LastSyncedAt   *time.Time `bun:"last_synced_at"`
	CreatedAt      time.Time  `bun:"created_at,notnull,default:current_timestamp"`
}

// SyncRun is an audit-log entry for a single reconciliation of a repository.
type SyncRun struct {
	bun.BaseModel `bun:"table:sync_runs,alias:sr"`

	ID               int64      `bun:"id,pk,autoincrement"`
	RepositoryID     int64      `bun:"repository_id,notnull"`
	StartedAt        time.Time  `bun:"started_at,notnull"`
	FinishedAt       *time.Time `bun:"finished_at"`
	Status           string     `bun:"status,notnull"`
	WorktreesCreated int        `bun:"worktrees_created,notnull"`
	WorktreesRemoved int        `bun:"worktrees_removed,notnull"`
	Error            *string    `bun:"error"`
}

// CreatedBy values for Worktree.CreatedBy, the safety rail that stops the
// daemon deleting a worktree a human made by hand.
const (
	CreatedByLumberjack  = "lumberjack"
	CreatedByPreexisting = "preexisting"
)

// Sync status values for Repository.LastSyncStatus and SyncRun.Status.
const (
	SyncStatusOK    = "ok"
	SyncStatusError = "error"
)
