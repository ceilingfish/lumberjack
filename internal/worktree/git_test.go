package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit runs git directly (bypassing the wrapper) for test setup, with
// deterministic identity/config so commits work in a clean environment.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{
		"-c", "user.email=test@example.com",
		"-c", "user.name=Test",
		"-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
		"-c", "protocol.file.allow=always", // permit file:// clones in tests
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setupRepos creates a bare "remote" and a working clone with an initial
// commit on main and a "feature/foo" branch pushed to the remote. It returns
// the wrapper, the working checkout path, and a fetch of origin already done.
func setupRepos(t *testing.T) (*Git, string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	main := filepath.Join(root, "main")

	runGit(t, root, "init", "--bare", remote)
	runGit(t, root, "clone", remote, main)

	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "initial")
	runGit(t, main, "push", "-u", "origin", "main")

	// A feature branch pushed to the remote, then back on main.
	runGit(t, main, "checkout", "-b", "feature/foo")
	if err := os.WriteFile(filepath.Join(main, "foo.txt"), []byte("foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "foo")
	runGit(t, main, "push", "-u", "origin", "feature/foo")
	runGit(t, main, "checkout", "main")

	g, err := NewGit()
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	return g, main
}

func TestNewGitEnvOverride(t *testing.T) {
	binPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv(EnvGitPath, binPath)
	g, err := NewGit()
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	if g.Path() != binPath {
		t.Errorf("Path = %q, want %q", g.Path(), binPath)
	}
}

func TestNewGitEnvOverrideMissing(t *testing.T) {
	t.Setenv(EnvGitPath, filepath.Join(t.TempDir(), "nope"))
	if _, err := NewGit(); err == nil {
		t.Fatal("expected error for missing git override")
	}
}

func TestGitVersion(t *testing.T) {
	g, err := NewGit()
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	v, err := g.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v == "" {
		t.Error("expected a version string")
	}
}

func TestGitRunError(t *testing.T) {
	g, _ := setupRepos(t)
	if _, err := g.run(context.Background(), t.TempDir(), "rev-parse", "HEAD"); err == nil {
		t.Fatal("expected error running git outside a repo")
	}
}

func TestRemoteURLAndFetch(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	url, err := g.RemoteURL(ctx, main, "origin")
	if err != nil {
		t.Fatalf("RemoteURL: %v", err)
	}
	if url == "" {
		t.Error("expected a remote URL")
	}
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestAddListRemoveWorktree(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// The tracked branch already exists locally in this clone, so exercise a
	// second branch that lives only on the remote to hit the --track path.
	runGit(t, main, "push", "origin", "feature/foo:feature/bar")
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	dir := filepath.Join(filepath.Dir(main), "wt-bar")
	if err := g.AddWorktree(ctx, main, dir, "origin", "feature/bar"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	refs, err := g.ListWorktrees(ctx, main)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	found := false
	for _, r := range refs {
		if filepath.Base(r.Dir) == "wt-bar" {
			found = true
			if r.Branch != "feature/bar" {
				t.Errorf("branch = %q, want feature/bar", r.Branch)
			}
		}
	}
	if !found {
		t.Errorf("new worktree not listed: %v", refs)
	}

	if err := g.RemoveWorktree(ctx, main, dir, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone, stat err = %v", err)
	}
}

func TestDefaultBranch(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	branch, err := g.DefaultBranch(ctx, main, "origin")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", branch, "main")
	}
}

func TestDefaultBranchFallsBackToRemote(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	// setupRepos clones the remote while it is still empty, so git never sets
	// the local origin/HEAD symbolic ref — every call here already exercises
	// the "ask the remote" fallback path.
	if _, err := g.run(ctx, main, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		t.Fatal("expected refs/remotes/origin/HEAD to be unset before the fallback runs")
	}

	branch, err := g.DefaultBranch(ctx, main, "origin")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", branch, "main")
	}
}

func TestShowFile(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	data, found, err := g.ShowFile(ctx, main, "origin/main", "README.md")
	if err != nil {
		t.Fatalf("ShowFile: %v", err)
	}
	if !found {
		t.Fatal("ShowFile: found = false, want true")
	}
	if string(data) != "hi\n" {
		t.Errorf("ShowFile content = %q, want %q", data, "hi\n")
	}
}

func TestShowFileMissing(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	_, found, err := g.ShowFile(ctx, main, "origin/main", "does-not-exist.yml")
	if err != nil {
		t.Fatalf("ShowFile: %v", err)
	}
	if found {
		t.Fatal("ShowFile: found = true, want false")
	}
}

func TestIsDirtyAndLocalOnlyCommits(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	dir := filepath.Join(filepath.Dir(main), "wt-foo")
	// feature/foo already exists locally; add it as a worktree by checkout.
	if err := g.AddWorktree(ctx, main, dir, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Clean and fully pushed: no dirt, no local-only commits.
	dirty, err := g.IsDirty(ctx, dir)
	if err != nil || dirty {
		t.Fatalf("IsDirty = %v, %v; want false", dirty, err)
	}
	n, err := g.LocalOnlyCommits(ctx, dir)
	if err != nil || n != 0 {
		t.Fatalf("LocalOnlyCommits = %d, %v; want 0", n, err)
	}

	// Make an uncommitted change: dirty.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirty, _ = g.IsDirty(ctx, dir); !dirty {
		t.Error("expected dirty after edit")
	}

	// Commit locally without pushing: one local-only commit.
	runGit(t, dir, "commit", "-am", "local work")
	if n, _ = g.LocalOnlyCommits(ctx, dir); n != 1 {
		t.Errorf("LocalOnlyCommits = %d, want 1", n)
	}
}

// TestMoveWorktree exercises the relocation `tidy` relies on against real git:
// the tree must land at the new path, git must report it there, and the moved
// worktree must still be a working checkout (its `.git` pointer rewritten).
func TestMoveWorktree(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	from := filepath.Join(filepath.Dir(main), "misplaced", "foo")
	if err := g.AddWorktree(ctx, main, from, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	to := filepath.Join(filepath.Dir(main), "main-foo")

	if err := g.MoveWorktree(ctx, main, from, to); err != nil {
		t.Fatalf("MoveWorktree: %v", err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("old worktree dir should be gone, stat err = %v", err)
	}

	refs, err := g.ListWorktrees(ctx, main)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	found := false
	for _, r := range refs {
		// Compare basenames: on macOS the temp dir git reports is the resolved
		// /private/var path, not the /var symlink the test built.
		if filepath.Base(r.Dir) == filepath.Base(to) {
			found = true
			if r.Branch != "feature/foo" {
				t.Errorf("branch = %q, want feature/foo", r.Branch)
			}
		}
	}
	if !found {
		t.Errorf("moved worktree not listed at %s: %v", to, refs)
	}

	// The relocated tree is still usable — a git command run inside it works.
	if _, err := g.IsDirty(ctx, to); err != nil {
		t.Errorf("moved worktree is not a working checkout: %v", err)
	}
}

// TestMoveWorktreeBuriesIntoAnExistingDestination pins the git behaviour that
// makes tidy's "destination already exists" check load-bearing: `git worktree
// move` onto an existing directory does not fail, it moves the worktree
// *inside* it (mv-style), landing it somewhere the caller did not ask for. A
// caller that skipped the check would record a path the worktree is not at.
func TestMoveWorktreeBuriesIntoAnExistingDestination(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	from := filepath.Join(filepath.Dir(main), "misplaced", "foo")
	if err := g.AddWorktree(ctx, main, from, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	to := filepath.Join(filepath.Dir(main), "occupied")
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := g.MoveWorktree(ctx, main, from, to); err != nil {
		t.Fatalf("MoveWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "foo", ".git")); err != nil {
		t.Errorf("expected the worktree buried at %s/foo, stat err = %v", to, err)
	}
	if _, err := os.Stat(filepath.Join(to, ".git")); !os.IsNotExist(err) {
		t.Errorf("worktree should not have landed at %s itself, stat err = %v", to, err)
	}
}
