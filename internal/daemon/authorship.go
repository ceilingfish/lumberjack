package daemon

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/authorship"
	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func (s *Service) identitiesFor(repo *schema.Repository) authorship.Identities {
	if v, ok := s.identities.Load(repo.ID); ok {
		if ids, ok := v.(authorship.Identities); ok {
			return ids
		}
	}
	return authorship.NewIdentities(repo.Login)
}

func (s *Service) refreshIdentities(ctx context.Context, repo *schema.Repository) {
	logins, err := s.gh.ListLogins(ctx, repo.Host)
	if err != nil {
		log.Printf("listing gh accounts for %s: %v", displayName(repo), err)
		return
	}
	s.identities.Store(repo.ID, authorship.NewIdentities(append(logins, repo.Login)...))
}

func (s *Service) resolveAuthorship(wt schema.Worktree, authors map[int64]string, ids authorship.Identities) schema.Authorship {
	in := authorship.Inputs{Override: wt.AuthorshipOverride, Origin: wt.Origin}
	if wt.GithubPRNumber != nil {
		in.PRAuthor, in.HasPR = authors[*wt.GithubPRNumber]
	}
	return authorship.Resolve(in, ids)
}

func (s *Service) SetAuthorship(ctx context.Context, repo *schema.Repository, ref string, a *schema.Authorship) (*WorktreeView, error) {
	id, err := s.setAuthorshipOverride(ctx, repo, ref, a)
	if err != nil {
		return nil, err
	}
	views, err := s.WorktreeViews(ctx, repo)
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].Worktree.ID == id {
			return &views[i], nil
		}
	}
	return nil, database.ErrWorktreeNotFound
}

func (s *Service) setAuthorshipOverride(ctx context.Context, repo *schema.Repository, ref string, a *schema.Authorship) (int64, error) {
	defer s.lockRepository(repo.ID)()
	wt, err := s.findWorktreeEnclosing(ctx, repo, ref)
	if err != nil {
		return 0, err
	}
	if err := s.db.SetWorktreeAuthorshipOverride(ctx, wt.ID, a); err != nil {
		return 0, err
	}
	return wt.ID, nil
}

func (s *Service) findWorktreeEnclosing(ctx context.Context, repo *schema.Repository, ref string) (*schema.Worktree, error) {
	wts, err := s.db.ListWorktrees(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	for i := range wts {
		if wts[i].Matches(ref) {
			return &wts[i], nil
		}
	}
	var best *schema.Worktree
	if filepath.IsAbs(ref) {
		clean := filepath.Clean(ref)
		for i := range wts {
			dir := wts[i].DirectoryPath
			if strings.HasPrefix(clean, dir+string(filepath.Separator)) && (best == nil || len(dir) > len(best.DirectoryPath)) {
				best = &wts[i]
			}
		}
	}
	if best == nil {
		return nil, database.ErrWorktreeNotFound
	}
	return best, nil
}
