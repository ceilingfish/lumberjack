package cmd

import (
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/spf13/cobra"
)

func newSetupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the current worktree's setup commands in order",
		Long: "Prints the run-command setup steps configured in the current " +
			"worktree's .lumberjack.yml, in the order the daemon would run them.",
		Args: cobra.NoArgs,
		RunE: runSetupList,
	}
}

func runSetupList(cmd *cobra.Command, _ []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	_, cfg, err := loadWorktreeConfig()
	if err != nil {
		return err
	}
	commands := cfg.RunCommands()
	out := cmd.OutOrStdout()
	if format == present.JSON {
		return present.WriteJSONArray(out, commands)
	}
	if len(commands) == 0 {
		_, err = fmt.Fprintln(out, "No setup commands configured.")
		return err
	}
	for _, command := range commands {
		if _, err := fmt.Fprintln(out, command); err != nil {
			return err
		}
	}
	return nil
}
