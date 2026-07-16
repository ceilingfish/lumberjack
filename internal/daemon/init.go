package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
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
		// A 404 against a GitHub remote is almost always the wrong credentials
		// rather than "not a GitHub repo" — surface a switch-credentials hint.
		if errors.Is(err, github.ErrRepoNotFound) {
			if hint := s.credentialHint(ctx, clean, remote); hint != "" {
				return nil, errors.New(hint)
			}
		}
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

// credentialHint builds the "you may need to switch credentials" message shown
// when gh returned a 404 for a checkout whose remote is a GitHub URL. It
// returns "" when the remote is not GitHub (so init falls back to the generic
// "not a GitHub repository" error) — a 404 there is genuinely not our concern.
func (s *Service) credentialHint(ctx context.Context, repoPath, remote string) string {
	rawURL, err := s.git.RemoteURL(ctx, repoPath, remote)
	if err != nil {
		return ""
	}
	slug, ok := githubRepoSlug(rawURL)
	if !ok {
		return ""
	}

	msg := fmt.Sprintf("cannot access the GitHub repository %s (remote %q)", slug, rawURL)
	if user, err := s.gh.AuthenticatedUser(ctx); err == nil && user != "" {
		msg += fmt.Sprintf("; gh is signed in as %q, which may not have access", user)
	} else {
		msg += "; the current gh credentials may not have access"
	}
	return msg + ".\nIf this is a personal repository on a different account, switch credentials " +
		"with `gh auth switch` (or `gh auth login`) and run `lumberjack init` again."
}

// githubRepoSlug extracts the "owner/name" of a GitHub remote from its URL,
// reporting false for non-GitHub remotes. It handles the scp-like
// (git@github.com:owner/name.git) and URL (https://github.com/owner/name.git,
// ssh://git@github.com/owner/name) forms a git remote takes.
func githubRepoSlug(raw string) (slug string, ok bool) {
	raw = strings.TrimSpace(raw)

	host, path := "", ""
	if scp := strings.SplitN(raw, "@", 2); !strings.Contains(raw, "://") && len(scp) == 2 && strings.Contains(scp[1], ":") {
		// scp-like: [user@]host:owner/name(.git)
		hostPath := strings.SplitN(scp[1], ":", 2)
		host, path = hostPath[0], hostPath[1]
	} else if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else {
		return "", false
	}

	if !isGitHubHost(host) {
		return "", false
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if owner, name, found := strings.Cut(path, "/"); found && owner != "" && name != "" {
		return owner + "/" + name, true
	}
	return "", false
}

// isGitHubHost reports whether host belongs to GitHub — github.com or a GitHub
// Enterprise host (conventionally github.<company>.com).
func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasPrefix(host, "github.")
}
