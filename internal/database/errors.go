package database

import "errors"

// Sentinel errors the daemon maps onto gRPC status codes (see
// internal/daemon/server.go). Callers compare with errors.Is.
var (
	// ErrRepositoryNotFound is returned when a repository reference (name or
	// path) matches no tracked repository.
	ErrRepositoryNotFound = errors.New("repository not found")
	// ErrRepositoryExists is returned when initialising a repository whose
	// local path is already tracked.
	ErrRepositoryExists = errors.New("repository already tracked")
	// ErrWorktreeNotFound is returned when a worktree reference matches no
	// tracked worktree for a repository.
	ErrWorktreeNotFound = errors.New("worktree not found")
)
