package github

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeClient builds a Client whose gh invocations are answered by fn, so tests
// exercise parsing/behaviour without a real gh binary.
func fakeClient(fn func(args ...string) (string, error)) *Client {
	return &Client{
		bin: "gh",
		run: func(_ context.Context, _ string, args ...string) (string, error) {
			return fn(args...)
		},
	}
}

func TestNewClientEnvOverride(t *testing.T) {
	binPath, err := exec.LookPath("gh")
	if err != nil {
		// Fall back to any executable so the override path is still exercised.
		binPath, err = exec.LookPath("sh")
		if err != nil {
			t.Skip("no executable available")
		}
	}
	t.Setenv(EnvCLIPath, binPath)
	c, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Path() != binPath {
		t.Errorf("Path = %q, want %q", c.Path(), binPath)
	}
}

func TestNewClientEnvOverrideMissing(t *testing.T) {
	t.Setenv(EnvCLIPath, filepath.Join(t.TempDir(), "nope"))
	if _, err := NewClient(); err == nil {
		t.Fatal("expected error for missing gh override")
	}
}

func TestVersion(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return "gh version 2.40.0 (2024-01-01)\nhttps://github.com/cli/cli", nil
	})
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != "2.40.0 (2024-01-01)" {
		t.Errorf("Version = %q", v)
	}
}

func TestAuthStatus(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "logged in", nil })
	if err := c.AuthStatus(context.Background()); err != nil {
		t.Errorf("AuthStatus: %v", err)
	}

	c = fakeClient(func(...string) (string, error) { return "", errors.New("not logged in") })
	if err := c.AuthStatus(context.Background()); err == nil {
		t.Error("expected auth error")
	}
}

func TestRepoInfo(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"owner":{"login":"ceilingfish"},"name":"Lumberjack",` +
			`"defaultBranchRef":{"name":"main"},"url":"https://github.com/ceilingfish/Lumberjack"}`, nil
	})
	info, err := c.RepoInfo(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("RepoInfo: %v", err)
	}
	want := RepoInfo{Owner: "ceilingfish", Name: "Lumberjack", Host: "github.com", DefaultBranch: "main"}
	if info != want {
		t.Errorf("RepoInfo = %+v, want %+v", info, want)
	}
}

func TestRepoInfoEnterpriseHost(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"owner":{"login":"acme"},"name":"widgets",` +
			`"defaultBranchRef":{"name":"trunk"},"url":"https://ghe.acme.corp/acme/widgets"}`, nil
	})
	info, _ := c.RepoInfo(context.Background(), "/repo")
	if info.Host != "ghe.acme.corp" {
		t.Errorf("Host = %q, want ghe.acme.corp", info.Host)
	}
}

func TestRepoInfoNotGitHub(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return "", errors.New("not a github repository")
	})
	if _, err := c.RepoInfo(context.Background(), "/repo"); err == nil {
		t.Error("expected error for non-GitHub repo")
	}
}

func TestRepoInfoNotFoundIsSentinel(t *testing.T) {
	cases := []string{
		"gh repo view: HTTP 404: Not Found (https://api.github.com/repos/o/n)",
		"gh repo view: GraphQL: Could not resolve to a Repository with the name 'o/n'. (repository)",
	}
	for _, stderr := range cases {
		c := fakeClient(func(...string) (string, error) { return "", errors.New(stderr) })
		_, err := c.RepoInfo(context.Background(), "/repo")
		if !errors.Is(err, ErrRepoNotFound) {
			t.Errorf("stderr %q: got %v, want ErrRepoNotFound", stderr, err)
		}
	}
}

func TestRepoInfoOtherErrorIsNotSentinel(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return "", errors.New("gh repo view: not a git repository")
	})
	_, err := c.RepoInfo(context.Background(), "/repo")
	if err == nil || errors.Is(err, ErrRepoNotFound) {
		t.Errorf("non-404 error should not be ErrRepoNotFound, got %v", err)
	}
}

func TestAuthenticatedUser(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return "octocat", nil
	})
	user, err := c.AuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("AuthenticatedUser: %v", err)
	}
	if user != "octocat" {
		t.Errorf("user = %q, want octocat", user)
	}
	if len(gotArgs) < 2 || gotArgs[0] != "api" || gotArgs[1] != "user" {
		t.Errorf("expected `api user` invocation, got %v", gotArgs)
	}
}

func TestActiveLogin(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return `{"hosts":{"github.com":[{"state":"success","active":true,` +
			`"host":"github.com","login":"ceilingfish"}]}}`, nil
	})
	login, err := c.ActiveLogin(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("ActiveLogin: %v", err)
	}
	if login != "ceilingfish" {
		t.Errorf("login = %q, want ceilingfish", login)
	}
	if len(gotArgs) < 3 || gotArgs[0] != "auth" || gotArgs[1] != "status" || gotArgs[2] != "--active" {
		t.Errorf("expected `auth status --active` invocation, got %v", gotArgs)
	}
}

func TestActiveLoginPicksActiveEntry(t *testing.T) {
	// Without --active narrowing, several accounts may be listed for a host; the
	// active one must win.
	c := fakeClient(func(...string) (string, error) {
		return `{"hosts":{"github.com":[` +
			`{"active":false,"login":"personal"},` +
			`{"active":true,"login":"work"}]}}`, nil
	})
	login, err := c.ActiveLogin(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("ActiveLogin: %v", err)
	}
	if login != "work" {
		t.Errorf("login = %q, want work", login)
	}
}

func TestActiveLoginNoAccountForHost(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"hosts":{"github.com":[{"active":true,"login":"x"}]}}`, nil
	})
	if _, err := c.ActiveLogin(context.Background(), "ghe.acme.corp"); err == nil {
		t.Error("expected error when host has no account")
	}
}

func TestActiveLoginBadJSON(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "not json", nil })
	if _, err := c.ActiveLogin(context.Background(), "github.com"); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestActiveLoginPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", errors.New("not logged in") })
	if _, err := c.ActiveLogin(context.Background(), "github.com"); err == nil {
		t.Error("expected error to propagate from gh")
	}
}

func TestSwitchAccount(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return "", nil
	})
	if err := c.SwitchAccount(context.Background(), "github.com", "work"); err != nil {
		t.Fatalf("SwitchAccount: %v", err)
	}
	want := []string{"auth", "switch", "--hostname", "github.com", "--user", "work"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args = %v, want %v", gotArgs, want)
			break
		}
	}
}

func TestRepoInfoBadJSON(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "not json", nil })
	if _, err := c.RepoInfo(context.Background(), "/repo"); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestListOpenPRs(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return `[{"number":42,"headRefName":"feature/x"},{"number":7,"headRefName":"fix/y"}]`, nil
	})
	repo := RepoInfo{Owner: "o", Name: "n", Host: "github.com"}
	prs, err := c.ListOpenPRs(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListOpenPRs: %v", err)
	}
	if len(prs) != 2 || prs[0].Number != 42 || prs[0].HeadBranch != "feature/x" {
		t.Errorf("prs = %+v", prs)
	}
	// The repo selector must be host/owner/name.
	joined := ""
	for _, a := range gotArgs {
		if a == "github.com/o/n" {
			joined = a
		}
	}
	if joined == "" {
		t.Errorf("expected -R github.com/o/n in args %v", gotArgs)
	}
}

func TestListOpenPRsBadJSON(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "{", nil })
	if _, err := c.ListOpenPRs(context.Background(), RepoInfo{}); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestExecRealBinaryError(t *testing.T) {
	// Exercise the real exec path against a binary that fails, so the stderr
	// wrapping is covered without a real gh.
	sh, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false binary")
	}
	c := &Client{bin: sh}
	c.run = c.exec
	if _, err := c.run(context.Background(), "", "anything"); err == nil {
		t.Error("expected error from failing binary")
	}
}
