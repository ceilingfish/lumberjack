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
		Long: "Adds, removes, and lists the run-command setup steps in the " +
			"current worktree's " + setup.ConfigFileName + ". These are the " +
			"commands the daemon runs against a freshly cloned worktree once " +
			"the config is merged to the default branch and consented to.",
	}
	c.AddCommand(newSetupAddCmd())
	c.AddCommand(newSetupRemoveCmd())
	c.AddCommand(newSetupListCmd())
	return c
}

// loadWorktreeConfig loads the setup config for the worktree the command was
// invoked from, returning the resolved root so callers can Save it back.
func loadWorktreeConfig() (root string, cfg *setup.Config, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	root, err = setup.RepoRoot(wd)
	if err != nil {
		return "", nil, err
	}
	cfg, err = setup.Load(root)
	if err != nil {
		return "", nil, err
	}
	return root, cfg, nil
}
