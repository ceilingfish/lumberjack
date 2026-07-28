package cmd

import (
	"fmt"
	"os"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/spf13/cobra"
)

func newSetupRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the current worktree's setup steps",
		Long: "Runs the setup steps governing the current worktree in order, " +
			"stopping at the first failure. A worktree with no " +
			setup.ConfigFileName + " of its own inherits the main checkout's, so " +
			"a freshly created worktree can be set up without one. Unlike the " +
			"daemon, this runs run-command steps without a separate consent " +
			"prompt — invoking it is the consent.",
		Args: cobra.NoArgs,
		RunE: runSetupRun,
	}
}

func runSetupRun(cmd *cobra.Command, _ []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	res, err := setup.Resolve(wd)
	if err != nil {
		return err
	}

	// Progress goes to stderr so `--format json` leaves stdout parseable.
	progress := cmd.ErrOrStderr()
	if len(res.Config.Steps) == 0 {
		return writeSetupMessage(cmd, format, "No setup steps configured.")
	}
	if res.Inherited {
		if _, err := fmt.Fprintf(progress, "Inheriting setup steps from %s\n", res.ConfigPath); err != nil {
			return err
		}
	}
	failedStep, runErr := setup.Run(cmd.Context(), res.Config, setup.Options{
		MainCheckout:   res.MainCheckout,
		WorktreeDir:    res.Worktree,
		Consented:      true,
		CommandTimeout: setup.ManualCommandTimeout,
		Output:         progress,
	})
	if runErr != nil {
		return fmt.Errorf("%s failed: %w", failedStep, runErr)
	}
	return writeSetupMessage(cmd, format,
		fmt.Sprintf("Ran %d setup step(s) in %s", len(res.Config.Steps), res.Worktree))
}

// writeSetupMessage prints a one-line outcome in the requested format, shared
// by the setup subcommands that only report what they did.
func writeSetupMessage(cmd *cobra.Command, format present.Format, msg string) error {
	if format == present.JSON {
		return present.WriteJSONObject(cmd.OutOrStdout(), setupResult{Message: msg})
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), msg)
	return err
}
