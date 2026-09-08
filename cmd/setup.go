package cmd

import (
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
	c.AddCommand(newSetupTrustCmd())
	return c
}
