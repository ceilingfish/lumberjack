package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "Register a repository for Lumberjack to track",
		Long: "Registers the git repository at [path] (default: the current " +
			"directory) so the daemon tracks its open PRs and reconciles " +
			"worktrees. The path must be a GitHub repository checkout.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolving path %q: %w", path, err)
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				repo, err := c.InitRepository(ctx, abs)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(),
					"Tracking %s/%s at %s\nWorktrees will be created under %s\n",
					repo.GetGithubOwner(), repo.GetGithubName(),
					repo.GetLocalPath(), repo.GetWorktreeParentDir())
				return err
			})
		},
	}
}
