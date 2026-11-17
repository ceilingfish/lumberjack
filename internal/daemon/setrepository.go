package daemon

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func (s *Service) SetRepository(ctx context.Context, repo *schema.Repository, owner, name string) (*schema.Repository, error) {
	if owner == "" || name == "" {
		return nil, fmt.Errorf("repository must be given as OWNER/NAME")
	}
	if strings.EqualFold(owner, repo.GithubOwner) && strings.EqualFold(name, repo.GithubName) {
		return repo, nil
	}

	defer s.lockRepository(repo.ID)()

	target := *repo
	target.GithubOwner, target.GithubName = owner, name
	if err := s.withRepoLogin(ctx, repo, func(ctx context.Context) error {
		return s.gh.CheckRepoAccess(ctx, repoInfo(&target))
	}); err != nil {
		return nil, fmt.Errorf("cannot access %s/%s on %s: %w", owner, name, repo.Host, err)
	}

	if err := s.db.ChangeGithubRepository(ctx, repo.ID, owner, name); err != nil {
		return nil, err
	}
	s.forgetPullRequests(repo.ID)
	repo.GithubOwner, repo.GithubName = owner, name
	return repo, nil
}

func (s *Service) forgetPullRequests(repoID int64) {
	s.rollups.Delete(repoID)
	prefix := strconv.FormatInt(repoID, 10) + "\x00"
	s.noPRBranches.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			s.noPRBranches.Delete(key)
		}
		return true
	})
}
