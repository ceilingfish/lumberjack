package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "status",
		Short: "Show last-sync detail for a repository",
		Long: "Shows last-sync detail for the tracked repository at the current " +
			"working directory, or for the repository named by --repository.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				repo, err := c.GetRepository(ctx, ref)
				if err != nil {
					return err
				}
				if err := emitRepositoryDetail(cmd.OutOrStdout(), format, repo); err != nil {
					return err
				}
				return promptSetupConsent(ctx, cmd, c, ref, repo)
			})
		},
	}

	addRepositoryFlag(c, &repository)
	return c
}

func setupConsentPending(repo *lumberjackv1.Repository) bool {
	steps := repo.GetSetupSteps()
	return len(steps.GetSteps()) > 0 && !steps.GetIsTrusted()
}

func promptSetupConsent(
	ctx context.Context, cmd *cobra.Command, cl *client.Client,
	ref string, repo *lumberjackv1.Repository,
) error {
	if !setupConsentPending(repo) {
		return nil
	}
	steps := repo.GetSetupSteps()

	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(out, "This repository's .lumberjack.yml runs the following command(s) on every new worktree:"); err != nil {
		return err
	}
	for _, c := range steps.GetSteps() {
		if _, err := fmt.Fprintf(out, "  %s\n", c); err != nil {
			return err
		}
	}
	if !confirm(cmd, "Allow Lumberjack to run these commands in new worktrees for this repository?") {
		_, err := fmt.Fprintln(out, "Not consented — the daemon will skip these commands until you run `lumberjack status` for this repository and consent.")
		return err
	}
	_, accepted, err := cl.SetSetupConsent(ctx, ref, steps.GetCurrentChecksum())
	if err != nil {
		return err
	}
	if !accepted {
		_, err := fmt.Fprintln(out, ".lumberjack.yml changed while you were reading it — nothing recorded. Run `lumberjack status` again to see the new steps.")
		return err
	}
	_, err = fmt.Fprintln(out, "Consent recorded.")
	return err
}
