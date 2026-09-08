package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/internal/cli"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "sync",
		Short: "Synchronise worktrees for a repository",
		Long: "Reconciles worktrees for the tracked repository at the current " +
			"working directory, or for the repository named by --repository, " +
			"against its open PRs. To sync every tracked repository, use " +
			"`lumberjack sync-all`.",
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
			return cli.WithClient(cmd, func(ctx context.Context, c *client.Client) error {
				return cli.RunSync(ctx, cmd, c, ref, format)
			})
		},
	}

	cli.AddRepositoryFlag(c, &repository)
	return c
}
