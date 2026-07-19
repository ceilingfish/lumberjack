package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show last-sync detail for the repository in the current directory",
		Long: "Shows the same detail as `lumberjack repositories NAME`, but for the " +
			"tracked repository at the current working directory. Run it from the " +
			"repo's checkout.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			abs, err := cwdAbs()
			if err != nil {
				return err
			}
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				repo, err := c.GetRepository(ctx, abs)
				if err != nil {
					return err
				}
				return emitRepositoryDetail(cmd.OutOrStdout(), format, repo)
			})
		},
	}
}
