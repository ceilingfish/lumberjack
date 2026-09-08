package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/internal/cli"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newWorktreesCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "worktrees",
		Short: "List a repository's worktrees",
		Long: "Lists worktrees for the tracked repository at the current working " +
			"directory, or for the repository named by --repository, with a " +
			"warning for any that need reconciliation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, err := cli.ResolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			return cli.WithClient(cmd, func(ctx context.Context, cl *client.Client) error {
				wts, err := cl.ListWorktrees(ctx, ref)
				if err != nil {
					return err
				}
				return cli.EmitWorktrees(cmd.OutOrStdout(), format, wts)
			})
		},
	}

	cli.AddRepositoryFlag(c, &repository)
	return c
}
