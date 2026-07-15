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

	dirs, err := g.ListWorktrees(ctx, main)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	found := false
	for _, d := range dirs {
		if filepath.Base(d) == "wt-bar" {
			found = true
		}
	}
	if !found {
		t.Errorf("new worktree not listed: %v", dirs)
	}

	if err := g.RemoveWorktree(ctx, main, dir, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone, stat err = %v", err)
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
