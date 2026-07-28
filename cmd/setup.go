package cmd

import (
	"os"

	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "setup-steps",
		Short: "Manage the current worktree's .lumberjack.yml setup commands",
		Long: "Adds, removes, lists, and runs the run-command setup steps in the " +
			"current worktree's " + setup.ConfigFileName + ". These are the " +
			"commands the daemon runs against a freshly cloned worktree once " +
			"the config is merged to the default branch and consented to. A " +
			"worktree without its own " + setup.ConfigFileName + " inherits the " +
			"main checkout's; writing one there overrides it.",
	}
	c.AddCommand(newSetupAddCmd())
	c.AddCommand(newSetupRemoveCmd())
	c.AddCommand(newSetupListCmd())
	c.AddCommand(newSetupRunCmd())
	return c
}

// loadWorktreeConfig loads the effective setup config for the worktree the
// command was invoked from — its own `.lumberjack.yml`, or the main checkout's
// when it has none — returning the worktree root callers Save back to. Editing
// an inherited config therefore materialises it as an override in this
// worktree, carrying the inherited steps with it rather than dropping them.
func loadWorktreeConfig() (root string, cfg *setup.Config, err error) {
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
