package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/internal/cli"
	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newSetAuthorshipCmd() *cobra.Command {
	var repository, worktree string

	c := &cobra.Command{
		Use:   "set-authorship mine|theirs|auto",
		Short: "Set whether a worktree is yours or someone else's",
		Long: "Overrides the derived authorship of a worktree, which decides whether " +
			"it is listed as yours or as incoming. The worktree is the one named by " +
			"--worktree (a branch or directory name), or the tracked worktree " +
			"containing the current directory. `auto` clears the override so " +
			"authorship is derived again.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: cli.AuthorshipArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			authorship, err := cli.ParseAuthorship(args[0])
			if err != nil {
				return err
			}
			ref, target, err := cli.ResolveWorktreeRef(repository, worktree)
			if err != nil {
				return err
			}
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			return cli.WithClient(cmd, func(ctx context.Context, cl *client.Client) error {
				wt, err := cl.SetAuthorship(ctx, ref, target, authorship)
				if err != nil {
					return err
				}
				return cli.EmitAuthorship(cmd.OutOrStdout(), format, wt)
			})
		},
	}

	c.Flags().StringVar(&worktree, "worktree", "", "the worktree to set, by branch or directory name (default: the one containing the current directory)")
	cli.AddRepositoryFlag(c, &repository)
	return c
}
