package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/internal/cli"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every repository Lumberjack is tracking",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			return cli.WithClient(cmd, func(ctx context.Context, cl *client.Client) error {
				repos, err := cl.ListRepositories(ctx)
				if err != nil {
					return err
				}
				return cli.EmitRepositories(cmd.OutOrStdout(), format, repos)
			})
		},
	}
}
