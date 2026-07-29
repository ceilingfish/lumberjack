package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func newTidyCmd() *cobra.Command {
	var (
		repository   string
		worktree     string
		dryRun       bool
		lockStrategy string
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
			"cannot be moved — its destination is occupied — is reported with the " +
			"reason, and the rest are still tidied. A worktree blocked by one that " +
			"this run moved away is tidied by running the command again.\n\n" +
			"git refuses to move a worktree it has locked, so tidy asks what to do " +
			"about each locked worktree it needs to move: unlock it for the move and " +
			"lock it again afterwards (the default), skip it, delete the lock, or " +
			"abort. --lock-strategy skip|unlock|delete|abort answers for every " +
			"locked worktree up front, and is required to move one when there is no " +
			"terminal to ask on — without it, locked worktrees are then reported as " +
			"skipped.\n\n" +
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
			strategy, err := parseLockStrategy(lockStrategy)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				opts := client.TidyOptions{
					Repository: ref, Worktree: worktree, DryRun: dryRun,
					LockStrategy: strategy,
				}
				// With no strategy given, ask about each locked worktree — but only
				// when there is a terminal to ask on. A dry run moves nothing, so
				// there is nothing to consent to: it reports locked worktrees as
				// skipped instead.
				if strategy == lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED && !dryRun && interactiveStdin() {
					decisions, err := resolveLockedWorktrees(ctx, cmd, cl, opts)
					if err != nil {
						return err
					}
					opts.LockDecisions = decisions
					// Anything locked that the user was not asked about — locked
					// between the two calls — stays put rather than being unlocked
					// unasked.
					opts.LockStrategy = lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP
				}
				moves, err := cl.Tidy(ctx, opts)
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
	c.Flags().StringVar(&lockStrategy, "lock-strategy", "",
		fmt.Sprintf("what to do with a locked worktree: %v (default: ask)", lockStrategyValues()))
	_ = c.RegisterFlagCompletionFunc("lock-strategy",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return lockStrategyValues(), cobra.ShellCompDirectiveNoFileComp
		})
	addRepositoryFlag(c, &repository)
	return c
}

// resolveLockedWorktrees asks the user what to do about every locked worktree
// this tidy would have to move, returning the answers keyed by the worktree's
// current directory. A nil map means nothing was locked and nothing was asked.
//
// It finds them with a dry run of the same tidy: a dry run reports every
// misplaced worktree along with the lock tidy found on it and — asked as if the
// locks were going to be lifted — leaves the error empty for exactly those whose
// only obstacle is the lock. A locked worktree that could not move anyway (its
// destination is occupied, say) is not worth a question.
func resolveLockedWorktrees(
	ctx context.Context, cmd *cobra.Command, cl *client.Client, opts client.TidyOptions,
) (map[string]lumberjackv1.LockStrategy, error) {
	probe := opts
	probe.DryRun = true
	probe.LockStrategy = lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK
	moves, err := cl.Tidy(ctx, probe)
	if err != nil {
		return nil, err
	}

	var decisions map[string]lumberjackv1.LockStrategy
	for _, m := range moves {
		if !m.GetLocked() || m.GetError() != "" {
			continue
		}
		strategy, err := lockPrompter(cmd, m.GetFrom(), m.GetLockReason())
		if err != nil {
			return nil, err
		}
		if strategy == lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT {
			// Abort cancels the whole tidy, not just this worktree, so nothing is
			// moved — not even the worktrees already answered for.
			return nil, fmt.Errorf("%w: %s", errLockAbort, m.GetFrom())
		}
		if decisions == nil {
			decisions = map[string]lumberjackv1.LockStrategy{}
		}
		decisions[m.GetFrom()] = strategy
	}
	return decisions, nil
}
