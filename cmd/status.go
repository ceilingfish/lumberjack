package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/internal/cli"
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
			ref, err := cli.ResolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			return cli.WithClient(cmd, func(ctx context.Context, c *client.Client) error {
				repo, err := c.GetRepository(ctx, ref)
				if err != nil {
					return err
				}
				if err := cli.EmitRepositoryDetail(cmd.OutOrStdout(), format, repo); err != nil {
					return err
				}
				return cli.PromptSetupConsent(ctx, cmd, c, ref, repo)
			})
		},
	}

	cli.AddRepositoryFlag(c, &repository)
	return c
}
