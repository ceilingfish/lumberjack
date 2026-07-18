// Package github wraps the GitHub CLI (`gh`). Lumberjack shells out to gh for
// all GitHub access rather than calling the API directly, so it inherits the
// user's `gh auth login` credentials and stores no tokens of its own (see
// docs/schema.md, "Not stored").
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// EnvCLIPath overrides the gh executable location; otherwise gh is found on
// PATH (see docs/prd.md environment variables).
const EnvCLIPath = "LUMBERJACK_GITHUB_CLI_PATH"

// ErrRepoNotFound is returned by RepoInfo when gh reaches GitHub but the
// repository is not visible to the authenticated account — either it does not
// exist or the current credentials lack access (an HTTP 404). It is distinct
// from a "not a GitHub checkout at all" failure so `lumberjack init` can tell
// the user they may need to switch gh credentials.
var ErrRepoNotFound = errors.New("repository not found or not accessible with the current GitHub credentials")

// Client runs the gh CLI. The command runner is indirected through run so the
// tests can drive Client without a real gh binary.
type Client struct {
	bin string
	run func(ctx context.Context, dir string, args ...string) (string, error)
}

// NewClient resolves the gh executable, honouring LUMBERJACK_GITHUB_CLI_PATH
// and otherwise searching PATH.
func NewClient() (*Client, error) {
	bin, err := resolveBinary(EnvCLIPath, "gh")
	if err != nil {
		return nil, err
	}
	c := &Client{bin: bin}
	c.run = c.exec
	return c, nil
}

// Path is the resolved absolute path to the gh binary (surfaced by doctor).
func (c *Client) Path() string { return c.bin }

// RepoInfo identifies a GitHub repository and its default branch.
type RepoInfo struct {
	Owner         string
	Name          string
	Host          string
	DefaultBranch string
}

// PR is the minimal live view of an open pull request: its number and the head
// branch a worktree tracks. Everything else about a PR is re-fetched when
// needed rather than cached (docs/schema.md).
type PR struct {
	Number     int64
	HeadBranch string
}

// exec runs gh with args in dir (empty dir = current directory), returning
// trimmed stdout. On failure the error carries gh's stderr.
func (c *Client) exec(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Version returns gh's reported version's first line (for doctor).
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return strings.TrimPrefix(out, "gh version "), nil
}

// AuthStatus verifies that gh is authenticated, returning gh's own error (which
// tells the user to run `gh auth login`) when it is not. Used by doctor.
func (c *Client) AuthStatus(ctx context.Context) error {
	_, err := c.run(ctx, "", "auth", "status")
	return err
}

// RepoInfo discovers the GitHub identity of the repository checked out at dir,
// via `gh repo view`. It errors if dir is not a GitHub repository — which is
// how `lumberjack init` rejects a non-GitHub checkout.
func (c *Client) RepoInfo(ctx context.Context, dir string) (RepoInfo, error) {
	out, err := c.run(ctx, dir, "repo", "view",
		"--json", "owner,name,defaultBranchRef,url")
	if err != nil {
		if isNotFound(err) {
			return RepoInfo{}, fmt.Errorf("%w: %v", ErrRepoNotFound, err)
		}
		return RepoInfo{}, err
	}
	var v struct {
		Owner            struct{ Login string } `json:"owner"`
		Name             string                 `json:"name"`
		DefaultBranchRef struct{ Name string }  `json:"defaultBranchRef"`
		URL              string                 `json:"url"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return RepoInfo{}, fmt.Errorf("parsing gh repo view: %w", err)
	}
	host := "github.com"
	if u, err := url.Parse(v.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	return RepoInfo{
		Owner:         v.Owner.Login,
		Name:          v.Name,
		Host:          host,
		DefaultBranch: v.DefaultBranchRef.Name,
	}, nil
}

// isNotFound reports whether a gh error is GitHub's "no such repository (for
// you)" response. gh phrases a 404 either as a REST "HTTP 404" or a GraphQL
// "Could not resolve to a Repository" message; both mean the repo either does
// not exist or is invisible to the authenticated account.
func isNotFound(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http 404") ||
		strings.Contains(msg, "404 not found") ||
		strings.Contains(msg, "could not resolve to a repository")
}

// AuthenticatedUser returns the login gh is currently signed in as, so callers
// can name the account when access to a repository is refused.
func (c *Client) AuthenticatedUser(ctx context.Context) (string, error) {
	return c.run(ctx, "", "api", "user", "--jq", ".login")
}

// ActiveLogin returns the login of the account gh currently has active for
// host (github.com or a GitHub Enterprise host). Lumberjack records this at
// init time and switches back to it before operating on a repository so
// account-switching (`gh auth switch`) is transparent to the user. It errors
// if no account is active for host.
func (c *Client) ActiveLogin(ctx context.Context, host string) (string, error) {
	out, err := c.run(ctx, "", "auth", "status", "--active", "--json", "hosts")
	if err != nil {
		return "", err
	}
	// `gh auth status --active --json hosts` shapes as
	//   {"hosts":{"<host>":[{"active":true,"login":"...","host":"..."}]}}
	// with --active narrowing each host's list to its active account.
	var v struct {
		Hosts map[string][]struct {
			Active bool   `json:"active"`
			Login  string `json:"login"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", fmt.Errorf("parsing gh auth status: %w", err)
	}
	accounts, ok := v.Hosts[host]
	if !ok {
		return "", fmt.Errorf("gh has no active account for %s", host)
	}
	for _, a := range accounts {
		if a.Active && a.Login != "" {
			return a.Login, nil
		}
	}
	// --active should already have filtered to the active account; fall back to
	// the first listed login so a gh output change can't silently break us.
	if len(accounts) > 0 && accounts[0].Login != "" {
		return accounts[0].Login, nil
	}
	return "", fmt.Errorf("gh has no active account for %s", host)
}

// ListLogins returns every gh account authenticated for host, in the order gh
// reports them. It is the set of logins a repository may be switched to; an
// empty slice means gh has no accounts for host at all.
func (c *Client) ListLogins(ctx context.Context, host string) ([]string, error) {
	out, err := c.run(ctx, "", "auth", "status", "--json", "hosts")
	if err != nil {
		return nil, err
	}
	// Without --active, `gh auth status --json hosts` lists every account per
	// host: {"hosts":{"<host>":[{"login":"...","host":"..."}, ...]}}.
	var v struct {
		Hosts map[string][]struct {
			Login string `json:"login"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("parsing gh auth status: %w", err)
	}
	var logins []string
	for _, a := range v.Hosts[host] {
		if a.Login != "" {
			logins = append(logins, a.Login)
		}
	}
	return logins, nil
}

// SwitchAccount makes login the active gh account for host
// (`gh auth switch`). Callers pair it with ActiveLogin to restore the prior
// account after an operation completes.
func (c *Client) SwitchAccount(ctx context.Context, host, login string) error {
	_, err := c.run(ctx, "", "auth", "switch", "--hostname", host, "--user", login)
	return err
}

// ListOpenPRs returns the open pull requests for repo. The limit is high
// enough to cover any realistic single repo; gh caps the page itself.
func (c *Client) ListOpenPRs(ctx context.Context, repo RepoInfo) ([]PR, error) {
	out, err := c.run(ctx, "", "pr", "list",
		"--repo", fmt.Sprintf("%s/%s/%s", repo.Host, repo.Owner, repo.Name),
		"--state", "open",
		"--limit", "1000",
		"--json", "number,headRefName")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number      int64  `json:"number"`
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr list: %w", err)
	}
	prs := make([]PR, len(raw))
	for i, r := range raw {
		prs[i] = PR{Number: r.Number, HeadBranch: r.HeadRefName}
	}
	return prs, nil
}

// resolveBinary returns the path from envVar if set (verified to exist),
// otherwise looks name up on PATH.
func resolveBinary(envVar, name string) (string, error) {
	if p := os.Getenv(envVar); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s=%q: %w", envVar, p, err)
		}
		return p, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH (set %s to override): %w", name, envVar, err)
	}
	return p, nil
}
