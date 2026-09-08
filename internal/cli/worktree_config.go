package cli

import (
	"os"

	"github.com/ceilingfish/lumberjack/internal/setup"
)

// LoadWorktreeConfig loads the effective setup config for the worktree the
// command was invoked from — its own `.lumberjack.yml`, or the main checkout's
// when it has none — returning the worktree root callers Save back to. Editing
// an inherited config therefore materialises it as an override in this
// worktree, carrying the inherited steps with it rather than dropping them.
func LoadWorktreeConfig() (root string, cfg *setup.Config, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	res, err := setup.Resolve(wd)
	if err != nil {
		return "", nil, err
	}
	return res.Worktree, res.Config, nil
}
