package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newRepositoriesCmd() *cobra.Command {
	var sync bool

	c := &cobra.Command{
		Use:   "repositories",
		Short: "List tracked repositories",
		Long: "Lists every repository Lumberjack is tracking. With --sync, first " +
			"triggers a synchronisation of all repositories and streams progress.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				if sync {
					// Empty ref syncs every tracked repository.
					return runSync(ctx, cmd, cl, "")
				}
				repos, err := cl.ListRepositories(ctx)
				if err != nil {
					return err
				}
				return renderRepositories(cmd.OutOrStdout(), repos)
			})
		},
	}

	c.Flags().BoolVar(&sync, "sync", false, "synchronise all repositories, streaming progress")
	return c
}
