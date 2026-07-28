package cmd

import (
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/spf13/cobra"
)

// setupResult is the json Format view model for `setup-steps add`/`setup-steps remove`,
// which otherwise only print a human-readable confirmation line.
type setupResult struct {
	Message string `json:"message"`
}

func newSetupAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add COMMAND",
		Short: "Add a setup command to the current worktree's .lumberjack.yml",
		Long: "Appends COMMAND as a run-command setup step in " +
			setup.ConfigFileName + ". Adding a command that is already present " +
			"is a no-op — the config never records a duplicate.",
		Args: cobra.ExactArgs(1),
		RunE: runSetupAdd,
	}
}

func runSetupAdd(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	command := args[0]
	root, cfg, err := loadWorktreeConfig()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("Setup command already present: %s", command)
	if cfg.AddCommand(command) {
		if err := setup.Save(root, cfg); err != nil {
			return err
		}
		msg = fmt.Sprintf("Added setup command: %s", command)
	}
	if format == present.JSON {
		return present.WriteJSONObject(cmd.OutOrStdout(), setupResult{Message: msg})
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
	return err
}
