package setup

import (
	"fmt"
	"os"
	"path/filepath"
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
