package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// worktreeRef finds the listed worktree whose directory ends in base. Basenames
// are compared for the reason TestMoveWorktree gives: git reports the resolved
// /private/var path on macOS, not the /var symlink the test built.
func worktreeRef(t *testing.T, g *Git, repoPath, base string) Ref {
	t.Helper()
	refs, err := g.ListWorktrees(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	for _, r := range refs {
		if filepath.Base(r.Dir) == base {
			return r
		}
	}
	t.Fatalf("worktree %s not listed: %v", base, refs)
	return Ref{}
}

// TestLockUnlockWorktree exercises the premise tidy's lock handling exists for
// against real git: a locked worktree refuses to move, the lock and its reason
// are visible in the listing, and unlocking makes the move possible again.
func TestLockUnlockWorktree(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	from := filepath.Join(filepath.Dir(main), "misplaced", "foo")
	if err := g.AddWorktree(ctx, main, from, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := g.LockWorktree(ctx, main, from, "keeping this one"); err != nil {
		t.Fatalf("LockWorktree: %v", err)
	}

	ref := worktreeRef(t, g, main, "foo")
	if !ref.Locked || ref.LockReason != "keeping this one" {
		t.Errorf("ref = %+v, want locked with the reason it was given", ref)
	}

	to := filepath.Join(filepath.Dir(main), "main-foo")
	if err := g.MoveWorktree(ctx, main, from, to); err == nil {
		t.Fatal("MoveWorktree succeeded on a locked worktree, want it refused")
	}

	if err := g.UnlockWorktree(ctx, main, from); err != nil {
		t.Fatalf("UnlockWorktree: %v", err)
	}
	if ref := worktreeRef(t, g, main, "foo"); ref.Locked {
		t.Errorf("ref = %+v, want unlocked", ref)
	}
	if err := g.MoveWorktree(ctx, main, from, to); err != nil {
		t.Fatalf("MoveWorktree after unlocking: %v", err)
	}

	// A lock with no reason: locked, but nothing to quote back to the user.
	if err := g.LockWorktree(ctx, main, to, ""); err != nil {
		t.Fatalf("LockWorktree without a reason: %v", err)
	}
	if ref := worktreeRef(t, g, main, "main-foo"); !ref.Locked || ref.LockReason != "" {
		t.Errorf("ref = %+v, want locked with no reason", ref)
	}
}

// git C-quotes a porcelain field whose value needs escaping, so a lock reason
// containing a newline arrives as `locked "agent busy\nsince tuesday"`. Left
// quoted it would be misreported and — worse — written back through
// `worktree lock --reason` when tidy restores a lock it lifted, escaping it one
// layer deeper each pass.
func TestUnquoteCStyleLockReason(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"unquoted is untouched", "in use", "in use"},
		{"newline", `"agent busy\nsince tuesday"`, "agent busy\nsince tuesday"},
		{"embedded quote", `"say \"hi\""`, `say "hi"`},
		{"tab", `"a\tb"`, "a\tb"},
		{"octal utf-8", `"caf\303\251"`, "café"},
		{"unparseable quoting is kept as-is", `"unterminated`, `"unterminated`},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unquoteCStyle(tc.in); got != tc.want {
				t.Errorf("unquoteCStyle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAddWorktreeFallsBackToAnExistingLocalBranch(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	runGit(t, main, "branch", "local-only")

	dir := filepath.Join(filepath.Dir(main), "wt-local")
	if err := g.AddWorktree(ctx, main, dir, "origin", "local-only"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := worktreeRef(t, g, main, "wt-local").Branch; got != "local-only" {
		t.Errorf("branch = %q, want local-only", got)
	}
}

func TestAddWorktreeReportsTheTrackingError(t *testing.T) {
	g, main := setupRepos(t)

	dir := filepath.Join(filepath.Dir(main), "wt-nope")
	err := g.AddWorktree(context.Background(), main, dir, "origin", "no-such-branch")
	if err == nil {
		t.Fatal("AddWorktree: expected an error for a branch that exists nowhere")
	}
	if !strings.Contains(err.Error(), "origin/no-such-branch") {
		t.Errorf("error %q is not the tracking failure", err)
	}
}

func TestAddWorktreeExistingTargetDirectory(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	empty := filepath.Join(filepath.Dir(main), "wt-empty")
	if err := os.MkdirAll(empty, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := g.AddWorktree(ctx, main, empty, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree into an existing empty directory: %v", err)
	}

	occupied := filepath.Join(filepath.Dir(main), "wt-occupied")
	if err := os.MkdirAll(occupied, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "stray.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.AddWorktree(ctx, main, occupied, "origin", "feature/foo"); err == nil {
		t.Error("AddWorktree into a non-empty directory succeeded, want it refused")
	}
}

func TestAddWorktreeNewBranch(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()
	if err := g.Fetch(ctx, main, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	dir := filepath.Join(filepath.Dir(main), "wt-new")
	if err := g.AddWorktreeNewBranch(ctx, main, dir, "origin/main", "feature/brand-new"); err != nil {
		t.Fatalf("AddWorktreeNewBranch: %v", err)
	}
	if got := worktreeRef(t, g, main, "wt-new").Branch; got != "feature/brand-new" {
		t.Errorf("branch = %q, want feature/brand-new", got)
	}

	again := filepath.Join(filepath.Dir(main), "wt-new-again")
	if err := g.AddWorktreeNewBranch(ctx, main, again, "origin/main", "feature/brand-new"); err == nil {
		t.Error("AddWorktreeNewBranch on an existing branch succeeded, want it refused")
	}
}

func TestRemoveWorktreeDirtyAndAlreadyGone(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	dirty := filepath.Join(filepath.Dir(main), "wt-dirty")
	if err := g.AddWorktree(ctx, main, dirty, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirty, "foo.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveWorktree(ctx, main, dirty, false); err == nil {
		t.Error("RemoveWorktree without force removed a dirty tree, want it refused")
	}
	if err := g.RemoveWorktree(ctx, main, dirty, true); err != nil {
		t.Fatalf("RemoveWorktree with force: %v", err)
	}

	gone := filepath.Join(filepath.Dir(main), "wt-gone")
	runGit(t, main, "worktree", "add", "--detach", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveWorktree(ctx, main, gone, false); err != nil {
		t.Fatalf("RemoveWorktree on a directory already deleted by hand: %v", err)
	}
}

func TestPullFastForwards(t *testing.T) {
	g, main := setupRepos(t)
	ctx := context.Background()

	dir := filepath.Join(filepath.Dir(main), "wt-foo")
	if err := g.AddWorktree(ctx, main, dir, "origin", "feature/foo"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	runGit(t, dir, "reset", "--hard", "HEAD~1")
	if _, err := os.Stat(filepath.Join(dir, "foo.txt")); !os.IsNotExist(err) {
		t.Fatalf("worktree is not behind its upstream, stat err = %v", err)
	}

	if err := g.Pull(ctx, dir); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "foo.txt")); err != nil {
		t.Errorf("Pull did not fast-forward the worktree: %v", err)
	}
}
