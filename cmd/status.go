package cmd

import (
	"context"

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
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				repo, err := c.GetRepository(ctx, ref)
				if err != nil {
					return err
				}
				return renderRepositoryDetail(cmd.OutOrStdout(), repo)
			})
		},
	}

	addRepositoryFlag(c, &repository)
	return c
}
