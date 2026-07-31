package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const helperEnv = "LUMBERJACK_TEST_GH_HELPER"

func TestGhHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("helper process only")
	}
	switch mode {
	case "stdout":
		fmt.Println("  gh version 2.40.0 (2024-01-01)  ")
	case "cwd":
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(wd)
	case "stderr":
		fmt.Fprintln(os.Stderr, "  gh: HTTP 404: Not Found  ")
		os.Exit(1)
	case "silent":
		os.Exit(3)
	}
	os.Exit(0)
}

func helperClient(t *testing.T, mode string) *Client {
	t.Helper()
	t.Setenv(helperEnv, mode)
	c := &Client{bin: os.Args[0]}
	c.run = c.exec
	return c
}

func helperArgs() []string {
	return []string{"-test.run=TestGhHelperProcess"}
}

func fakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing fake binary: %v", err)
	}
	return p
}

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
	binPath := fakeBinary(t, t.TempDir(), "gh")
	t.Setenv(EnvCLIPath, binPath)
	c, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Path() != binPath {
		t.Errorf("Path = %q, want %q", c.Path(), binPath)
	}
	if c.run == nil {
		t.Error("NewClient must wire the command runner")
	}
}

func TestNewClientFindsGhOnPath(t *testing.T) {
	dir := t.TempDir()
	want := fakeBinary(t, dir, "gh")
	t.Setenv(EnvCLIPath, "")
	t.Setenv("PATH", dir)
	c, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Path() != want {
		t.Errorf("Path = %q, want %q", c.Path(), want)
	}
}

func TestNewClientGhNotOnPath(t *testing.T) {
	t.Setenv(EnvCLIPath, "")
	t.Setenv("PATH", t.TempDir())
	c, err := NewClient()
	if err == nil {
		t.Fatalf("expected an error when gh is absent from PATH, got client %+v", c)
	}
	if c != nil {
		t.Errorf("client = %+v, want nil", c)
	}
	msg := err.Error()
	for _, want := range []string{"gh", "PATH", EnvCLIPath} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q so the daemon can tell the user what to fix", msg, want)
		}
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error should wrap exec.ErrNotFound, got %v", err)
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

func TestCheckRepoAccess(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return `{"name":"Lumberjack"}`, nil
	})
	repo := RepoInfo{Owner: "ceilingfish", Name: "Lumberjack", Host: "github.com"}
	if err := c.CheckRepoAccess(context.Background(), repo); err != nil {
		t.Fatalf("CheckRepoAccess: %v", err)
	}
	// It must target the specific repo (HOST/OWNER/NAME), not the cwd.
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "github.com/ceilingfish/Lumberjack") {
		t.Errorf("args = %v, want a HOST/OWNER/NAME target", gotArgs)
	}
}

func TestCheckRepoAccessNotFoundIsSentinel(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return "", errors.New("gh repo view: HTTP 404: Not Found")
	})
	repo := RepoInfo{Owner: "o", Name: "n", Host: "github.com"}
	if err := c.CheckRepoAccess(context.Background(), repo); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("got %v, want ErrRepoNotFound", err)
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

func TestExecTrimsStdout(t *testing.T) {
	c := helperClient(t, "stdout")
	out, err := c.run(context.Background(), "", helperArgs()...)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if out != "gh version 2.40.0 (2024-01-01)" {
		t.Errorf("out = %q, want trimmed stdout", out)
	}
}

func TestExecRunsInDir(t *testing.T) {
	dir := t.TempDir()
	c := helperClient(t, "cwd")
	out, err := c.run(context.Background(), dir, helperArgs()...)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	got, err := filepath.EvalSymlinks(out)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", out, err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	if got != want {
		t.Errorf("cwd = %q, want %q", got, want)
	}
}

func TestExecReportsStderr(t *testing.T) {
	c := helperClient(t, "stderr")
	out, err := c.run(context.Background(), "", helperArgs()...)
	if err == nil {
		t.Fatal("expected an error from a failing gh")
	}
	if out != "" {
		t.Errorf("out = %q, want empty on failure", out)
	}
	if !strings.Contains(err.Error(), "HTTP 404: Not Found") {
		t.Errorf("error %q should carry gh's stderr", err)
	}
	if !strings.Contains(err.Error(), strings.Join(helperArgs(), " ")) {
		t.Errorf("error %q should name the gh invocation", err)
	}
}

func TestExecFallsBackToExitError(t *testing.T) {
	c := helperClient(t, "silent")
	_, err := c.run(context.Background(), "", helperArgs()...)
	if err == nil {
		t.Fatal("expected an error from a failing gh")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("with no stderr the exit status must surface, got %q", err)
	}
}

func TestExecCancelledContext(t *testing.T) {
	c := helperClient(t, "stdout")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.run(ctx, "", helperArgs()...); err == nil {
		t.Error("expected an error for a cancelled context")
	}
}

func TestVersionWithoutTrailingLines(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "gh version 2.1.0", nil })
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != "2.1.0" {
		t.Errorf("Version = %q, want 2.1.0", v)
	}
}

func TestVersionPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", errors.New("gh not executable") })
	v, err := c.Version(context.Background())
	if err == nil {
		t.Fatal("expected the gh failure to propagate")
	}
	if v != "" {
		t.Errorf("Version = %q, want empty on error", v)
	}
}

func TestAuthStatusUnauthenticatedIsNotNotFound(t *testing.T) {
	cases := map[string]string{
		"unauthenticated": "You are not logged into any GitHub hosts. To log in, run: gh auth login",
		"network":         "dial tcp: lookup api.github.com: no such host",
	}
	for name, stderr := range cases {
		t.Run(name, func(t *testing.T) {
			c := fakeClient(func(...string) (string, error) { return "", errors.New(stderr) })
			err := c.AuthStatus(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), stderr) {
				t.Errorf("error %q should carry gh's own remedy text", err)
			}
			if errors.Is(err, ErrRepoNotFound) {
				t.Error("must not be reported as ErrRepoNotFound")
			}
			if isNotFound(err) {
				t.Error("must not be classified as a 404")
			}
		})
	}
}

func TestRepoInfoEmptyOutput(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", nil })
	if _, err := c.RepoInfo(context.Background(), "/repo"); err == nil {
		t.Error("expected an error for empty gh output")
	}
}

func TestRepoInfoUnparseableURLFallsBackToGitHubCom(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"owner":{"login":"o"},"name":"n","defaultBranchRef":{"name":"main"},"url":"::not a url"}`, nil
	})
	info, err := c.RepoInfo(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("RepoInfo: %v", err)
	}
	if info.Host != "github.com" {
		t.Errorf("Host = %q, want the github.com fallback", info.Host)
	}
}

func TestCheckRepoAccessPropagatesOtherErrors(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return "", errors.New("gh repo view: dial tcp: connection refused")
	})
	err := c.CheckRepoAccess(context.Background(), RepoInfo{Owner: "o", Name: "n", Host: "github.com"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrRepoNotFound) {
		t.Errorf("a network failure must not be ErrRepoNotFound, got %v", err)
	}
}

func TestAuthenticatedUserPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", errors.New("gh auth required") })
	user, err := c.AuthenticatedUser(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if user != "" {
		t.Errorf("user = %q, want empty on error", user)
	}
}

func TestActiveLoginFallsBackToFirstListedAccount(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"hosts":{"github.com":[{"active":false,"login":"first"},{"active":false,"login":"second"}]}}`, nil
	})
	login, err := c.ActiveLogin(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("ActiveLogin: %v", err)
	}
	if login != "first" {
		t.Errorf("login = %q, want first", login)
	}
}

func TestActiveLoginEmptyAccountList(t *testing.T) {
	for name, out := range map[string]string{
		"no entries":  `{"hosts":{"github.com":[]}}`,
		"blank login": `{"hosts":{"github.com":[{"active":true,"login":""}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := fakeClient(func(...string) (string, error) { return out, nil })
			if _, err := c.ActiveLogin(context.Background(), "github.com"); err == nil {
				t.Error("expected an error when no login is usable")
			}
		})
	}
}

func TestListLogins(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return `{"hosts":{"github.com":[{"login":"personal"},{"login":""},{"login":"work"}],` +
			`"ghe.acme.corp":[{"login":"employee"}]}}`, nil
	})
	logins, err := c.ListLogins(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("ListLogins: %v", err)
	}
	if want := []string{"personal", "work"}; !reflect.DeepEqual(logins, want) {
		t.Errorf("logins = %v, want %v", logins, want)
	}
	want := []string{"auth", "status", "--json", "hosts"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v (no --active, so every account is listed)", gotArgs, want)
	}
}

func TestListLoginsUnknownHost(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"hosts":{"github.com":[{"login":"personal"}]}}`, nil
	})
	logins, err := c.ListLogins(context.Background(), "ghe.acme.corp")
	if err != nil {
		t.Fatalf("ListLogins: %v", err)
	}
	if len(logins) != 0 {
		t.Errorf("logins = %v, want empty for a host gh knows nothing about", logins)
	}
}

func TestListLoginsBadJSON(t *testing.T) {
	for name, out := range map[string]string{"garbage": "not json", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			c := fakeClient(func(...string) (string, error) { return out, nil })
			logins, err := c.ListLogins(context.Background(), "github.com")
			if err == nil {
				t.Fatalf("expected a parse error, got %v", logins)
			}
			if !strings.Contains(err.Error(), "parsing gh auth status") {
				t.Errorf("error %q should say what failed to parse", err)
			}
		})
	}
}

func TestListLoginsPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", errors.New("not logged in") })
	logins, err := c.ListLogins(context.Background(), "github.com")
	if err == nil {
		t.Fatal("expected the gh failure to propagate")
	}
	if logins != nil {
		t.Errorf("logins = %v, want nil on error", logins)
	}
}

func TestSwitchAccountPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return "", errors.New("no account work for github.com")
	})
	if err := c.SwitchAccount(context.Background(), "github.com", "work"); err == nil {
		t.Error("expected the gh failure to propagate")
	}
}

func TestListOpenPRsEmpty(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "[]", nil })
	prs, err := c.ListOpenPRs(context.Background(), RepoInfo{Owner: "o", Name: "n", Host: "github.com"})
	if err != nil {
		t.Fatalf("ListOpenPRs: %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("prs = %+v, want none", prs)
	}
}

func TestListOpenPRsPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", errors.New("API rate limit exceeded") })
	prs, err := c.ListOpenPRs(context.Background(), RepoInfo{})
	if err == nil {
		t.Fatal("expected the gh failure to propagate")
	}
	if prs != nil {
		t.Errorf("prs = %+v, want nil on error", prs)
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error %q should carry gh's reason", err)
	}
}

func TestPRMerged(t *testing.T) {
	cases := map[string]bool{"MERGED": true, "OPEN": false, "CLOSED": false}
	for state, want := range cases {
		t.Run(state, func(t *testing.T) {
			var gotArgs []string
			c := fakeClient(func(args ...string) (string, error) {
				gotArgs = args
				return fmt.Sprintf(`{"state":%q}`, state), nil
			})
			repo := RepoInfo{Owner: "o", Name: "n", Host: "github.com"}
			merged, err := c.PRMerged(context.Background(), repo, 42)
			if err != nil {
				t.Fatalf("PRMerged: %v", err)
			}
			if merged != want {
				t.Errorf("PRMerged(%s) = %v, want %v", state, merged, want)
			}
			wantArgs := []string{"pr", "view", "42", "--repo", "github.com/o/n", "--json", "state"}
			if !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Errorf("args = %v, want %v", gotArgs, wantArgs)
			}
		})
	}
}

func TestPRMergedPropagatesError(t *testing.T) {
	c := fakeClient(func(...string) (string, error) { return "", errors.New("gh pr view: HTTP 404") })
	merged, err := c.PRMerged(context.Background(), RepoInfo{}, 1)
	if err == nil {
		t.Fatal("expected the gh failure to propagate")
	}
	if merged {
		t.Error("merged must be false on error")
	}
}

func TestPRMergedBadJSON(t *testing.T) {
	for name, out := range map[string]string{"garbage": "<html>", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			c := fakeClient(func(...string) (string, error) { return out, nil })
			merged, err := c.PRMerged(context.Background(), RepoInfo{}, 1)
			if err == nil {
				t.Fatal("expected a parse error")
			}
			if merged {
				t.Error("merged must be false on a parse error")
			}
			if !strings.Contains(err.Error(), "parsing gh pr view") {
				t.Errorf("error %q should say what failed to parse", err)
			}
		})
	}
}
