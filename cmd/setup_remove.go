package cmd

import (
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/spf13/cobra"
)

func newSetupRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove COMMAND",
		Short: "Remove a setup command from the current worktree's .lumberjack.yml",
		Long: "Removes the run-command setup step running COMMAND from " +
			setup.ConfigFileName + ". It errors if no such command is " +
			"configured, so a mistyped removal fails rather than silently " +
			"doing nothing.",
		Args: cobra.ExactArgs(1),
		// COMMAND completes to the commands currently configured.
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			_, cfg, err := loadWorktreeConfig()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return cfg.RunCommands(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: runSetupRemove,
	}
}

func runSetupRemove(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	command := args[0]
	root, cfg, err := loadWorktreeConfig()
	if err != nil {
		return err
	}
	if !cfg.RemoveCommand(command) {
		return fmt.Errorf("no setup command matching %q", command)
	}
	if err := setup.Save(root, cfg); err != nil {
		return err
	}
	msg := fmt.Sprintf("Removed setup command: %s", command)
	if format == present.JSON {
		return present.WriteJSONObject(cmd.OutOrStdout(), setupResult{Message: msg})
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
	return err
}
