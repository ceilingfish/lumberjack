package daemon

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// InitRepository registers the repository checked out at localPath: it
// confirms the checkout is a GitHub repo (via gh), derives the worktree naming
// defaults, and stores a repository row. It returns database.ErrRepositoryExists
// if the path is already tracked.
//
// localPath must be absolute; the CLI resolves "." before calling.
func (s *Service) InitRepository(ctx context.Context, localPath string) (*schema.Repository, error) {
	clean := filepath.Clean(localPath)

	remote, err := s.git.DefaultRemote(ctx, clean)
	if err != nil {
		return nil, err
	}
	info, err := s.gh.RepoInfo(ctx, clean)
	if err != nil {
		return nil, fmt.Errorf("%s is not a GitHub repository: %w", clean, err)
	}

	repo := &schema.Repository{
		LocalPath: clean,
		// Sibling worktrees live next to the main checkout by default.
		WorktreeParentDir: filepath.Dir(clean),
		// dir_prefix defaults to the checkout's folder name but is stored so a
		// later folder rename can't change worktree naming (docs/schema.md).
		DirPrefix:     filepath.Base(clean),
		GithubOwner:   info.Owner,
		GithubName:    info.Name,
		DefaultRemote: remote,
		Host:          info.Host,
	}
	if err := s.db.CreateRepository(ctx, repo); err != nil {
		return nil, err
	}
	return repo, nil
}
