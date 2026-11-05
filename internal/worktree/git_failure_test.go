package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubGit(t *testing.T, script string) *Git {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "git-stub")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Git{bin: bin}
}

func absentGit(t *testing.T) *Git {
	t.Helper()
	return &Git{bin: filepath.Join(t.TempDir(), "no-such-git")}
}

func TestRunSurfacesGitStderr(t *testing.T) {
	g := stubGit(t, `echo "fatal: could not read from remote repository" >&2; exit 128`)

	err := g.Fetch(context.Background(), t.TempDir(), "origin")
	if err == nil {
		t.Fatal("Fetch: expected an error")
	}
	if !strings.Contains(err.Error(), "fatal: could not read from remote repository") {
		t.Errorf("error %q does not carry git's stderr", err)
	}
	if !strings.Contains(err.Error(), "git fetch --prune origin") {
		t.Errorf("error %q does not name the failing command", err)
	}
}

func TestRunFallsBackToExecErrorWhenStderrIsEmpty(t *testing.T) {
	g := absentGit(t)

	_, err := g.Version(context.Background())
	if err == nil {
		t.Fatal("Version: expected an error")
	}
	if !strings.Contains(err.Error(), "git --version") {
		t.Errorf("error %q does not name the failing command", err)
	}
	if strings.HasSuffix(err.Error(), ": ") {
		t.Errorf("error %q has no diagnostic at all", err)
	}
}

func TestDefaultRemote(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		wantErr            string
	}{
		{name: "prefers origin", script: `echo upstream; echo origin`, want: "origin"},
		{name: "falls back to the first remote", script: `echo upstream; echo fork`, want: "upstream"},
		{name: "no remotes", script: `exit 0`, wantErr: "has no git remotes"},
		{name: "git fails", script: `echo "fatal: not a git repository" >&2; exit 128`, wantErr: "fatal: not a git repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stubGit(t, tc.script).DefaultRemote(context.Background(), t.TempDir())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("DefaultRemote error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DefaultRemote: %v", err)
			}
			if got != tc.want {
				t.Errorf("DefaultRemote = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDefaultBranchSetHeadFails(t *testing.T) {
	g := stubGit(t, `echo "fatal: unable to reach the remote" >&2; exit 128`)

	_, err := g.DefaultBranch(context.Background(), t.TempDir(), "origin")
	if err == nil {
		t.Fatal("DefaultBranch: expected an error")
	}
	if !strings.Contains(err.Error(), "determining default branch for remote origin") {
		t.Errorf("error %q does not say which remote failed", err)
	}
	if !strings.Contains(err.Error(), "fatal: unable to reach the remote") {
		t.Errorf("error %q does not carry git's stderr", err)
	}
}

func TestDefaultBranchRetryAfterSetHeadFails(t *testing.T) {
	g := stubGit(t, `case "$1" in
symbolic-ref) echo "fatal: ref refs/remotes/origin/HEAD is not a symbolic ref" >&2; exit 128 ;;
*) exit 0 ;;
esac`)

	_, err := g.DefaultBranch(context.Background(), t.TempDir(), "origin")
	if err == nil {
		t.Fatal("DefaultBranch: expected an error when the retry also fails")
	}
	if !strings.Contains(err.Error(), "determining default branch for remote origin") {
		t.Errorf("error %q does not say which remote failed", err)
	}
}

func TestShowFileFallsBackToExecErrorWhenStderrIsEmpty(t *testing.T) {
	_, found, err := absentGit(t).ShowFile(context.Background(), t.TempDir(), "origin/main", "cfg.yml")
	if err == nil {
		t.Fatal("ShowFile: expected an error")
	}
	if found {
		t.Error("ShowFile: found = true on failure")
	}
	if !strings.Contains(err.Error(), "git show origin/main:cfg.yml") {
		t.Errorf("error %q does not name the failing command", err)
	}
}

func TestShowFileSurfacesGitStderr(t *testing.T) {
	g := stubGit(t, `echo "fatal: invalid object name 'origin/nope'" >&2; exit 128`)

	_, found, err := g.ShowFile(context.Background(), t.TempDir(), "origin/nope", "README.md")
	if err == nil {
		t.Fatal("ShowFile: expected an error for an unknown ref")
	}
	if found {
		t.Error("ShowFile: found = true on failure")
	}
	if !strings.Contains(err.Error(), "fatal: invalid object name 'origin/nope'") {
		t.Errorf("error %q does not carry git's stderr", err)
	}
}

func TestShowFileMissingPathVariants(t *testing.T) {
	for _, stderr := range []string{
		"fatal: path 'cfg.yml' does not exist in 'origin/main'",
		"fatal: path 'cfg.yml' exists on disk, but not in 'origin/main'",
	} {
		t.Run(stderr, func(t *testing.T) {
			g := stubGit(t, `echo "`+stderr+`" >&2; exit 128`)
			data, found, err := g.ShowFile(context.Background(), t.TempDir(), "origin/main", "cfg.yml")
			if err != nil {
				t.Fatalf("ShowFile: %v", err)
			}
			if found || data != nil {
				t.Errorf("ShowFile = %q, %v; want no data and found = false", data, found)
			}
		})
	}
}

func TestListWorktreesParsesPorcelain(t *testing.T) {
	g := stubGit(t, `cat <<'EOF'
worktree /repo
bare

worktree /repo/wt-open
HEAD 1111111111111111111111111111111111111111
branch refs/heads/feature/foo

worktree /repo/wt-detached
HEAD 2222222222222222222222222222222222222222
detached

worktree /repo/wt-locked
HEAD 3333333333333333333333333333333333333333
branch refs/heads/feature/bar
locked "agent busy\nsince tuesday"
EOF`)

	refs, err := g.ListWorktrees(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	want := []Ref{
		{Dir: "/repo"},
		{Dir: "/repo/wt-open", Branch: "feature/foo"},
		{Dir: "/repo/wt-detached"},
		{Dir: "/repo/wt-locked", Branch: "feature/bar", Locked: true, LockReason: "agent busy\nsince tuesday"},
	}
	if len(refs) != len(want) {
		t.Fatalf("ListWorktrees = %+v, want %d entries", refs, len(want))
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, refs[i], want[i])
		}
	}
}

func TestListWorktreesIgnoresFieldsBeforeAnyWorktree(t *testing.T) {
	g := stubGit(t, `printf 'branch refs/heads/feature/foo\nlocked\n'`)

	refs, err := g.ListWorktrees(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("ListWorktrees = %+v, want no entries", refs)
	}
}

func TestListWorktreesError(t *testing.T) {
	g := stubGit(t, `echo "fatal: not a git repository" >&2; exit 128`)

	if _, err := g.ListWorktrees(context.Background(), t.TempDir()); err == nil {
		t.Fatal("ListWorktrees: expected an error")
	}
}

func TestLocalOnlyCommitsErrors(t *testing.T) {
	for _, tc := range []struct {
		name, script, wantErr string
	}{
		{"git fails", `echo "fatal: not a git repository" >&2; exit 128`, "fatal: not a git repository"},
		{"unparseable count", `echo "lots"`, `parsing rev-list count "lots"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := stubGit(t, tc.script).LocalOnlyCommits(context.Background(), t.TempDir(), "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LocalOnlyCommits error = %v, want one containing %q", err, tc.wantErr)
			}
			if n != 0 {
				t.Errorf("LocalOnlyCommits = %d on failure, want 0", n)
			}
		})
	}
}

func TestResolveBinaryNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := resolveBinary(EnvGitPath, "lumberjack-no-such-binary")
	if err == nil {
		t.Fatal("resolveBinary: expected an error")
	}
	if !strings.Contains(err.Error(), "not found on PATH") || !strings.Contains(err.Error(), EnvGitPath) {
		t.Errorf("error %q should name the binary and the override variable", err)
	}
}

func TestWorkingState(t *testing.T) {
	for _, tc := range []struct {
		name, script, wantBranch, wantErr string
		wantDirty                         bool
	}{
		{name: "clean on a branch", script: `printf '# branch.oid abc\n# branch.head feature/foo\n# branch.upstream origin/feature/foo\n'`, wantBranch: "feature/foo"},
		{name: "dirty on a branch", script: `printf '# branch.head feature/foo\n1 .M N... 100644 100644 100644 a b foo.txt\n'`, wantBranch: "feature/foo", wantDirty: true},
		{name: "untracked file", script: `printf '# branch.head main\n? new.txt\n'`, wantBranch: "main", wantDirty: true},
		{name: "detached HEAD", script: `printf '# branch.oid abc\n# branch.head (detached)\n'`},
		{name: "git fails", script: `echo "fatal: not a git repository" >&2; exit 128`, wantErr: "fatal: not a git repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dirty, branch, err := stubGit(t, tc.script).WorkingState(context.Background(), t.TempDir())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("WorkingState error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("WorkingState: %v", err)
			}
			if dirty != tc.wantDirty || branch != tc.wantBranch {
				t.Errorf("WorkingState = %v, %q; want %v, %q", dirty, branch, tc.wantDirty, tc.wantBranch)
			}
		})
	}
}

func TestUserEmail(t *testing.T) {
	if got := stubGit(t, `echo me@example.com`).UserEmail(context.Background(), t.TempDir()); got != "me@example.com" {
		t.Errorf("UserEmail = %q, want me@example.com", got)
	}
	if got := stubGit(t, `exit 1`).UserEmail(context.Background(), t.TempDir()); got != "" {
		t.Errorf("UserEmail on failure = %q, want empty", got)
	}
}

func TestLocalOnlyCommitsCommitterFilter(t *testing.T) {
	g := stubGit(t, `echo "$@" >&2; case "$*" in *"--committer=<me@example.com>"*) echo 2;; *) echo 5;; esac`)
	if n, err := g.LocalOnlyCommits(context.Background(), t.TempDir(), "me@example.com"); err != nil || n != 2 {
		t.Errorf("LocalOnlyCommits with committer = %d, %v; want 2", n, err)
	}
	if n, err := g.LocalOnlyCommits(context.Background(), t.TempDir(), ""); err != nil || n != 5 {
		t.Errorf("LocalOnlyCommits without committer = %d, %v; want 5", n, err)
	}
}
