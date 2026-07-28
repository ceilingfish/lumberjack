package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepoRoot returns the root of the git working tree containing start, i.e. the
// nearest ancestor holding a `.git` entry. `.git` is a directory in a normal
// checkout and a file in a linked worktree, so both the main checkout and any
// `git worktree` land on their own root — which is where `.lumberjack.yml`
// lives for that worktree. It errors when start is not inside a git working
// tree, since there is no worktree config to author there.
func RepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s is not inside a git repository", start)
		}
		dir = parent
	}
}

// MainCheckout returns the main working tree of the repository whose worktree
// root is root. A normal checkout holds a `.git` directory and is its own main
// checkout; a linked worktree holds a `.git` file pointing at
// `<main>/.git/worktrees/<name>`, which names the main checkout.
func MainCheckout(root string) (string, error) {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return root, nil
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", gitPath, err)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if gitDir == "" {
		return "", fmt.Errorf("%s has no gitdir pointer", gitPath)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	// Walk up from <main>/.git/worktrees/<name> to the `.git` directory; its
	// parent is the main checkout.
	for dir := filepath.Clean(gitDir); ; {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s points outside a repository: %s", gitPath, gitDir)
		}
		if filepath.Base(dir) == ".git" {
			return parent, nil
		}
		dir = parent
	}
}

// Resolved is the setup config governing a worktree, and the paths its steps
// resolve against.
type Resolved struct {
	// Worktree is the root of the worktree the config governs — run-command's
	// working directory and copy-file's destination root.
	Worktree string
	// MainCheckout is the repository's main working tree — copy-file's source
	// root, so untracked local files (e.g. .env) can be picked up. It equals
	// Worktree in the main checkout itself.
	MainCheckout string
	// ConfigPath is the file Config was read from, or "" when neither the
	// worktree nor the main checkout has one.
	ConfigPath string
	// Inherited reports whether Config came from the main checkout rather than
	// the worktree's own file.
	Inherited bool
	// Config is the effective config; empty (not nil) when there is no file.
	Config *Config
}

// Resolve resolves the effective setup config for the worktree containing dir.
// A linked worktree inherits the main checkout's `.lumberjack.yml`, so a fresh
// worktree needs no config of its own; a `.lumberjack.yml` in the worktree
// itself overrides the inherited one wholesale rather than merging with it, so
// what runs is always exactly one file's worth of steps.
func Resolve(dir string) (*Resolved, error) {
	root, err := RepoRoot(dir)
	if err != nil {
		return nil, err
	}
	main, err := MainCheckout(root)
	if err != nil {
		return nil, err
	}
	res := &Resolved{Worktree: root, MainCheckout: main, Config: &Config{}}

	cfg, found, err := loadIfPresent(configPath(root))
	if err != nil {
		return nil, err
	}
	if found {
		res.ConfigPath, res.Config = configPath(root), cfg
		return res, nil
	}
	if main == root {
		return res, nil
	}
	cfg, found, err = loadIfPresent(configPath(main))
	if err != nil {
		return nil, err
	}
	if found {
		res.ConfigPath, res.Config, res.Inherited = configPath(main), cfg, true
	}
	return res, nil
}
