package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newSetupTrustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trust",
		Short: "Trust the current worktree's setup steps",
		Long: "Records the checksum of the " + setup.ConfigFileName + " governing " +
			"this worktree as one you have agreed to run, so `setup-steps run` " +
			"and the daemon run its commands without asking again. Every " +
			"checksum trusted this way stays trusted, so switching between " +
			"branches with different setup steps only asks once per version.",
		Args: cobra.NoArgs,
		RunE: runSetupTrust,
	}
}

func runSetupTrust(cmd *cobra.Command, _ []string) error {
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
	if res.ConfigPath == "" {
		return fmt.Errorf("no %s governs %s", setup.ConfigFileName, res.Worktree)
	}
	ref, err := cwdAbs()
	if err != nil {
		return err
	}
	return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
		if _, err := cl.TrustSetupSteps(ctx, ref, setup.Fingerprint(res.Raw)); err != nil {
			return err
		}
		return present.WriteMessage(cmd.OutOrStdout(), format, "Trusted the setup steps in "+res.ConfigPath)
	})
}
