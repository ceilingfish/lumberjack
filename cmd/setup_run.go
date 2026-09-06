package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func newSetupRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the current worktree's setup steps",
		Long: "Runs the setup steps governing the current worktree in order, " +
			"stopping at the first failure. A worktree with no " +
			setup.ConfigFileName + " of its own inherits the main checkout's, so " +
			"a freshly created worktree can be set up without one. Run-commands " +
			"run without prompting when the config matches the repository's " +
			"trusted default-branch " + setup.ConfigFileName + "; when it differs " +
			"— an unreviewed local edit — the commands are shown and confirmed " +
			"first.",
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
	consented, err := consentToRunCommands(cmd, progress, res)
	if err != nil {
		return err
	}
	failedStep, runErr := setup.Run(cmd.Context(), res.Config, setup.Options{
		MainCheckout:   res.MainCheckout,
		WorktreeDir:    res.Worktree,
		Consented:      consented,
		CommandTimeout: setup.ManualCommandTimeout,
		Output:         progress,
	})
	if runErr != nil {
		return fmt.Errorf("%s failed: %w", failedStep, runErr)
	}
	return writeSetupMessage(cmd, format,
		fmt.Sprintf("Ran %d setup step(s) in %s", len(res.Config.Steps), res.Worktree))
}

func consentToRunCommands(cmd *cobra.Command, out io.Writer, res *setup.Resolved) (bool, error) {
	if !res.Config.HasRunCommands() {
		return false, nil
	}
	trusted, reason := trustedSetupChecksum(cmd)
	if reason == "" && trusted != "" && trusted == setup.Fingerprint(res.Raw) {
		return true, nil
	}

	if reason == "" {
		reason = res.ConfigPath + " differs from the version on the default branch"
	}
	if _, err := fmt.Fprintf(out, "%s, so its command(s) have not been reviewed:\n", reason); err != nil {
		return false, err
	}
	for _, c := range res.Config.RunCommands() {
		if _, err := fmt.Fprintf(out, "  %s\n", c); err != nil {
			return false, err
		}
	}
	if confirmOn(cmd, out, "Run these commands here now?") {
		return true, nil
	}
	_, err := fmt.Fprintln(out, "Skipping run-command steps; other steps still run.")
	return false, err
}

func trustedSetupChecksum(cmd *cobra.Command) (checksum, reason string) {
	ref, err := cwdAbs()
	if err != nil {
		return "", "the current directory could not be resolved"
	}
	var steps *lumberjackv1.SetupSteps
	err = withClient(cmd, func(ctx context.Context, cl *client.Client) error {
		repo, err := cl.GetRepository(ctx, ref)
		if err != nil {
			return err
		}
		steps = repo.GetSetupSteps()
		return nil
	})
	if err != nil {
		return "", "this repository's trusted " + setup.ConfigFileName + " could not be read (" + err.Error() + ")"
	}
	if !steps.GetIsDefined() {
		return "", "the default branch has no " + setup.ConfigFileName
	}
	return steps.GetCurrentChecksum(), ""
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
