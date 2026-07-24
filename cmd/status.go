package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/pkg/client"
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
				if repo.GetSetupConsentPending() {
					return promptSetupConsent(ctx, cmd, c, ref)
				}
				return nil
			})
		},
	}

	addRepositoryFlag(c, &repository)
	return c
}

// promptSetupConsent checks whether the repository resolved by ref has
// `.lumberjack.yml` run-command setup steps pending the local user's consent
// and, if so, shows the commands and asks for it (trust-on-first-use). It is
// a no-op when nothing is pending, so callers can invoke it unconditionally
// after `init` or whenever a repository's detail is shown.
func promptSetupConsent(ctx context.Context, cmd *cobra.Command, cl *client.Client, ref string) error {
	pending, commands, err := cl.GetSetupConsent(ctx, ref)
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}

	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(out, "This repository's .lumberjack.yml runs the following command(s) on every new worktree:"); err != nil {
		return err
	}
	for _, c := range commands {
		if _, err := fmt.Fprintf(out, "  %s\n", c); err != nil {
			return err
		}
	}
	if !confirm(cmd, "Allow Lumberjack to run these commands in new worktrees for this repository?") {
		_, err := fmt.Fprintln(out, "Not consented — the daemon will skip these commands until you run `lumberjack status` for this repository and consent.")
		return err
	}
	if _, err := cl.SetSetupConsent(ctx, ref); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Consent recorded.")
	return err
}
