package daemon

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/authorship"
	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
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

func (s *Service) resolveAuthorship(repo *schema.Repository, wt schema.Worktree, prs map[int64]schema.PullRequest, ids authorship.Identities) schema.Authorship {
	in := authorship.Inputs{Override: wt.AuthorshipOverride, Origin: wt.Origin}
	if wt.GithubPRNumber != nil {
		var pr schema.PullRequest
		pr, in.HasPR = prs[*wt.GithubPRNumber]
		in.PRAuthor = pr.AuthorLogin
	}
	if !in.HasPR {
		in.Commits = s.commitsVerdict(repo, wt.BranchName, ids)
	}
	return authorship.Resolve(in, ids)
}

func branchKey(repo *schema.Repository, branch string) string {
	return fmt.Sprintf("%d\x00%s", repo.ID, branch)
}

func (s *Service) commitsVerdict(repo *schema.Repository, branch string, ids authorship.Identities) *schema.Authorship {
	head, ok := s.branchHeads.Load(branchKey(repo, branch))
	if !ok {
		return nil
	}
	verdict := schema.AuthorshipMine
	if sha, _ := head.(string); sha != "" {
		cached, ok := s.commitAuthors.Load(sha)
		if !ok {
			return nil
		}
		ca, _ := cached.(github.CommitAuthors)
		verdict = authorship.CommitsVerdict(ca.Logins, ca.Truncated, ids)
	}
	return &verdict
}

func needsCommitAuthors(wt schema.Worktree, prs map[int64]schema.PullRequest) bool {
	if wt.GithubPRNumber != nil {
		if _, ok := prs[*wt.GithubPRNumber]; ok {
			return false
		}
	}
	return wt.AuthorshipOverride == nil && wt.Origin != schema.OriginLocal && wt.BranchName != ""
}

func (s *Service) branchesNeedingAuthors(ctx context.Context, repo *schema.Repository, stored []schema.Worktree) []string {
	prs, err := s.db.PullRequests(ctx, repo.ID)
	if err != nil {
		log.Printf("listing pull requests of %s: %v", displayName(repo), err)
		return nil
	}
	var branches []string
	for _, wt := range stored {
		if needsCommitAuthors(wt, prs) {
			branches = append(branches, wt.BranchName)
		}
	}
	return branches
}

func (s *Service) refreshBranchAuthors(ctx context.Context, repo *schema.Repository, stored []schema.Worktree) {
	base := ""
	for _, branch := range s.branchesNeedingAuthors(ctx, repo, stored) {
		sha, err := s.git.RemoteBranchSHA(ctx, repo.LocalPath, repo.DefaultRemote, branch)
		if err != nil {
			log.Printf("resolving %s on %s: %v", branch, displayName(repo), err)
			continue
		}
		s.branchHeads.Store(branchKey(repo, branch), sha)
		if _, cached := s.commitAuthors.Load(sha); sha == "" || cached {
			continue
		}
		if base == "" {
			if base, err = s.git.DefaultBranch(ctx, repo.LocalPath, repo.DefaultRemote); err != nil {
				log.Printf("resolving the default branch of %s: %v", displayName(repo), err)
				return
			}
		}
		authors, err := s.gh.CompareCommitAuthors(ctx, repoInfo(repo), base, sha)
		if err != nil {
			log.Printf("listing commit authors of %s on %s: %v", branch, displayName(repo), err)
			continue
		}
		s.commitAuthors.Store(sha, authors)
	}
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
