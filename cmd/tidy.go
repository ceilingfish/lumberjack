package cmd

import (
	"context"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newTidyCmd() *cobra.Command {
	var (
		repository string
		worktree   string
		dryRun     bool
	)

	c := &cobra.Command{
		Use:   "tidy",
		Short: "Move misplaced worktrees back to their idiomatic locations",
		Long: "Checks where every tracked worktree of the repository at the current " +
			"working directory — or the one named by --repository — actually lives, " +
			"and moves any that are not in the location Lumberjack's naming " +
			"convention gives them: <worktrees dir>/<repository name>-<branch " +
			"slug>, the layout `lumberjack sync` creates. A worktree checked out by " +
			"hand somewhere else (under `.claude/worktrees/`, say) is relocated " +
			"with `git worktree move` and its tracked location updated.\n\n" +
			"Worktrees already in place are left alone and not reported. One that " +
			"cannot be moved — its destination is occupied, or git has it locked — " +
			"is reported with the reason, and the rest are still tidied. A worktree " +
			"blocked by one that this run moved away is tidied by running the " +
			"command again.\n\n" +
			"--worktree BRANCH_OR_DIR narrows the tidy to a single worktree. The " +
			"repository's other worktrees still hold their directories, so a " +
			"narrowed tidy can never move one worktree onto another's path.\n\n" +
			"Use --dry-run to see what would move without moving anything.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				moves, err := cl.Tidy(ctx, ref, worktree, dryRun)
				if err != nil {
					return err
				}
				return emitTidyMoves(cmd.OutOrStdout(), format, moves, dryRun)
			})
		},
	}

	c.Flags().StringVar(&worktree, "worktree", "",
		"tidy only this worktree, named by its branch or directory")
	c.Flags().BoolVar(&dryRun, "dry-run", false,
		"report what would move without moving anything")
	addRepositoryFlag(c, &repository)
	return c
}
