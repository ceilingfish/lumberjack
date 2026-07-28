package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// EnvGitPath overrides the git executable location; otherwise git is found on
// PATH (see docs/prd.md environment variables).
const EnvGitPath = "LUMBERJACK_GIT_PATH"

// Git runs the system git binary. Lumberjack shells out rather than linking a
// git library so the binary stays self-contained and inherits the user's git
// configuration (AGENTS.md, "single self-contained binary").
type Git struct {
	bin string
}

// NewGit resolves the git executable, honouring LUMBERJACK_GIT_PATH and
// otherwise searching PATH. It returns an error if git cannot be found so the
// daemon fails loudly at startup rather than at first worktree operation.
func NewGit() (*Git, error) {
	bin, err := resolveBinary(EnvGitPath, "git")
	if err != nil {
		return nil, err
	}
	return &Git{bin: bin}, nil
}

// Path is the resolved absolute path to the git binary (surfaced by doctor).
func (g *Git) Path() string { return g.bin }

// run executes git with args in dir (empty dir = current process directory),
// returning trimmed stdout. On failure the error carries git's stderr, which
// is where git writes its diagnostics.
func (g *Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Version returns git's reported version (for doctor).
func (g *Git) Version(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(out, "git version "), nil
}

// RemoteURL returns the fetch URL configured for remote in repoPath.
func (g *Git) RemoteURL(ctx context.Context, repoPath, remote string) (string, error) {
	return g.run(ctx, repoPath, "remote", "get-url", remote)
}

// DefaultRemote returns the name of the remote to sync against: "origin" if it
// exists, otherwise the first configured remote. It errors when the repo has
// no remotes (it cannot be tracked without one).
func (g *Git) DefaultRemote(ctx context.Context, repoPath string) (string, error) {
	out, err := g.run(ctx, repoPath, "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(out)
	if len(remotes) == 0 {
		return "", fmt.Errorf("%s has no git remotes", repoPath)
	}
	for _, r := range remotes {
		if r == "origin" {
			return "origin", nil
		}
	}
	return remotes[0], nil
}

// Fetch updates remote-tracking refs (and prunes deleted branches) so
// reconciliation compares against the current remote state.
func (g *Git) Fetch(ctx context.Context, repoPath, remote string) error {
	_, err := g.run(ctx, repoPath, "fetch", "--prune", remote)
	return err
}

// Pull fast-forwards the checkout at dir to its upstream (`git pull --ff-only`).
// It never creates a merge commit: a branch that has diverged from or has no
// upstream makes git fail, which the caller treats as "nothing to pull" rather
// than an error. Callers must confirm the tree is clean first, since a
// fast-forward still touches the working files.
func (g *Git) Pull(ctx context.Context, dir string) error {
	_, err := g.run(ctx, dir, "pull", "--ff-only")
	return err
}

// AddWorktree creates a worktree at dir checked out to branch, tracking
// remote/branch. It fetches nothing itself — the caller fetches first. The
// created worktree is left on a local branch tracking the remote head.
func (g *Git) AddWorktree(ctx context.Context, repoPath, dir, remote, branch string) error {
	// Prefer creating a local branch that tracks the remote head. If the local
	// branch already exists (e.g. re-adding after a manual removal), fall back
	// to checking it out directly.
	_, err := g.run(ctx, repoPath, "worktree", "add", "--track",
		"-b", branch, dir, remote+"/"+branch)
	if err == nil {
		return nil
	}
	if _, err2 := g.run(ctx, repoPath, "worktree", "add", dir, branch); err2 != nil {
		// Report the original (tracking) error, which is the common case.
		return err
	}
	return nil
}

// MoveWorktree relocates the worktree at from to to, letting git rewrite both
// the worktree's `.git` file and the admin directory's `gitdir` pointer. git
// refuses when the worktree is locked or when from is the main working tree —
// conditions the caller reports rather than works around.
//
// It does NOT refuse when to already exists: like `mv`, git then moves the
// worktree *inside* that directory, landing it at to/<basename of from>
// instead of at to. Callers must therefore check that to is free before
// calling, or they will record a location the worktree is not at.
func (g *Git) MoveWorktree(ctx context.Context, repoPath, from, to string) error {
	_, err := g.run(ctx, repoPath, "worktree", "move", from, to)
	return err
}

// RemoveWorktree removes the worktree at dir. force drops the safety checks
// git applies for dirty or diverged trees; the daemon only sets it after the
// caller has confirmed the loss (see DeleteWorktree in the proto).
func (g *Git) RemoveWorktree(ctx context.Context, repoPath, dir string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, dir)
	_, err := g.run(ctx, repoPath, args...)
	return err
}

// DefaultBranch returns the short name of remote's default branch (e.g.
// "main"), used to read the trusted setup-steps config from the base branch
// rather than whatever branch a worktree is being cloned for. It relies on
// the local `refs/remotes/<remote>/HEAD` symbolic ref, set by `git clone` and
// by `git remote set-head`; if it is missing (e.g. the checkout predates
// Lumberjack or wasn't made with `git clone`), it asks the remote directly
// (`git remote set-head --auto`) and retries once.
func (g *Git) DefaultBranch(ctx context.Context, repoPath, remote string) (string, error) {
	out, err := g.run(ctx, repoPath, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		if _, serr := g.run(ctx, repoPath, "remote", "set-head", remote, "--auto"); serr != nil {
			return "", fmt.Errorf("determining default branch for remote %s: %w", remote, err)
		}
		out, err = g.run(ctx, repoPath, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
		if err != nil {
			return "", fmt.Errorf("determining default branch for remote %s: %w", remote, err)
		}
	}
	return strings.TrimPrefix(out, remote+"/"), nil
}

// ShowFile reads path as it exists at ref (e.g. "origin/main"), via `git
// show`. It returns found=false (with no error) when the ref exists but the
// path does not — the caller's config file is simply absent there, which is
// not itself a failure.
func (g *Git) ShowFile(ctx context.Context, repoPath, ref, path string) (data []byte, found bool, err error) {
	cmd := exec.CommandContext(ctx, g.bin, "show", ref+":"+path)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "does not exist in") || strings.Contains(msg, "exists on disk, but not in") {
			return nil, false, nil
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, false, fmt.Errorf("git show %s:%s: %s", ref, path, msg)
	}
	return stdout.Bytes(), true, nil
}

// Ref is one entry from `git worktree list`: the worktree's directory and the
// branch checked out there. Branch is empty for a detached-HEAD worktree. It
// lets the sync engine match an already-checked-out directory to the PR whose
// branch it holds and adopt it rather than trying to recreate it.
type Ref struct {
	Dir    string
	Branch string
}

// ListWorktrees returns every worktree registered on repoPath (including the
// main working tree), parsed from `git worktree list --porcelain`.
func (g *Git) ListWorktrees(ctx context.Context, repoPath string) ([]Ref, error) {
	out, err := g.run(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var (
		refs []Ref
		cur  *Ref
	)
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			refs = append(refs, Ref{Dir: p})
			cur = &refs[len(refs)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
			cur.Branch = b
		}
	}
	return refs, nil
}

// IsDirty reports whether the worktree at dir has uncommitted changes
// (tracked modifications or untracked files).
func (g *Git) IsDirty(ctx context.Context, dir string) (bool, error) {
	out, err := g.run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// LocalOnlyCommits counts commits reachable from the worktree's HEAD but from
// no remote-tracking branch — i.e. local work that exists nowhere on the
// remote. It is the "commits you would lose" figure used both to decide
// whether an orphaned worktree needs reconciliation and to warn on delete.
//
// The caller must Fetch first so remote-tracking refs are current; otherwise
// commits already pushed can be miscounted as local-only.
func (g *Git) LocalOnlyCommits(ctx context.Context, dir string) (int64, error) {
	out, err := g.run(ctx, dir, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing rev-list count %q: %w", out, err)
	}
	return n, nil
}

// resolveBinary returns the path from envVar if set, otherwise looks name up on
// PATH. A path set via envVar is verified to exist so a stale override fails
// with a clear message.
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
