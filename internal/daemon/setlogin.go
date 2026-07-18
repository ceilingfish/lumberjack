package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// SetLogin sets the gh account repo operates under: the daemon switches to it
// (`gh auth switch`) before any git/gh operation on the repo and restores the
// prior account afterwards (see withRepoLogin). It persists the change and
// returns the updated repository row.
//
// login must be non-empty and must be an account gh is authenticated as for the
// repo's host — otherwise the switch would fail later, at sync time, far from
// where the mistake was made. There is no "clear" here because a repo with no
// login silently skips account switching, which set-login exists to fix.
func (s *Service) SetLogin(ctx context.Context, repo *schema.Repository, login string) (*schema.Repository, error) {
	if login == "" {
		return nil, fmt.Errorf("login must not be empty")
	}

	logins, err := s.gh.ListLogins(ctx, repo.Host)
	if err != nil {
		return nil, fmt.Errorf("listing gh accounts for %s: %w", repo.Host, err)
	}
	if !slices.Contains(logins, login) {
		return nil, fmt.Errorf("%q is not a gh account authenticated for %s (available: %s); run `gh auth login` first",
			login, repo.Host, available(logins))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// A login gh knows about is not necessarily one that can operate this repo:
	// switch to it and confirm it can reach the repo on GitHub before committing.
	if err := s.withLogin(ctx, repo.Host, login, func() error {
		return s.gh.CheckRepoAccess(ctx, repoInfo(repo))
	}); err != nil {
		return nil, fmt.Errorf("account %q cannot access %s/%s on %s: %w",
			login, repo.GithubOwner, repo.GithubName, repo.Host, err)
	}

	if err := s.db.UpdateLogin(ctx, repo.ID, login); err != nil {
		return nil, err
	}
	repo.Login = login
	return repo, nil
}

// ListLogins returns the gh accounts authenticated for repo's host — the
// candidates SetLogin accepts.
func (s *Service) ListLogins(ctx context.Context, repo *schema.Repository) ([]string, error) {
	return s.gh.ListLogins(ctx, repo.Host)
}

// available renders a login list for an error message, or "none" when empty.
func available(logins []string) string {
	if len(logins) == 0 {
		return "none"
	}
	return strings.Join(logins, ", ")
}
