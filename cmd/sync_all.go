package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/internal/cli"
	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newSyncAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync-all",
		Short: "Synchronise worktrees for every tracked repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			return cli.WithClient(cmd, func(ctx context.Context, c *client.Client) error {
				// Empty ref syncs every tracked repository.
				return cli.RunSync(ctx, cmd, c, "", format)
			})
		},
	}
}
