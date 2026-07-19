package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/color"
	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Synchronise worktrees for the repository in the current directory",
		Long: "Reconciles worktrees for the tracked repository at the current " +
			"working directory against its open PRs. Run it from the repo's " +
			"main checkout. To sync every tracked repository, use " +
			"`lumberjack repositories --sync`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			abs, err := cwdAbs()
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				return runSync(ctx, cmd, c, abs)
			})
		},
	}
}

// runSync streams a sync of ref (empty = all repositories) and renders, per
// repository, a branch/PR/action table of the changes followed by a summary
// line. Shared by `sync` and `repositories --sync`.
func runSync(ctx context.Context, cmd *cobra.Command, c *client.Client, ref string) error {
	out := cmd.OutOrStdout()
	// Buffer each repo's per-branch changes so the table can be column-aligned
	// and printed once the repo completes. Repos stream sequentially, but keying
	// by name keeps this correct regardless of interleaving.
	changes := map[string][]*lumberjackv1.WorktreeChange{}
	return c.Sync(ctx, ref, func(e *lumberjackv1.SyncResponse) error {
		repo := e.GetRepository()
		if !e.GetCompleted() {
			if ch := e.GetChange(); ch != nil {
				changes[repo] = append(changes[repo], ch)
			} else if msg := e.GetMessage(); msg != "" {
				if _, err := fmt.Fprintf(out, "%s: %s\n", repo, msg); err != nil {
					return err
				}
			}
			return nil
		}
		if err := renderWorktreeChanges(out, changes[repo]); err != nil {
			return err
		}
		delete(changes, repo)
		s := e.GetSummary()
		_, err := fmt.Fprintf(out, "%s: %s (+%d worktree(s), -%d)%s\n",
			repo, paintSummaryStatus(s),
			s.GetWorktreesCreated(), s.GetWorktreesRemoved(), summaryError(s))
		return err
	})
}

func summaryStatus(s *lumberjackv1.SyncSummary) string {
	if s.GetStatus() == lumberjackv1.SyncStatus_SYNC_STATUS_ERROR {
		return "error"
	}
	return "synced"
}

// paintSummaryStatus colourises summaryStatus: error red, everything else
// (currently just "synced") ok green. This is a standalone Fprintf line, not
// a tabwriter cell, so it can be coloured directly with no alignment concern.
func paintSummaryStatus(s *lumberjackv1.SyncSummary) string {
	if s.GetStatus() == lumberjackv1.SyncStatus_SYNC_STATUS_ERROR {
		return color.Error(summaryStatus(s))
	}
	return color.OK(summaryStatus(s))
}

func summaryError(s *lumberjackv1.SyncSummary) string {
	if s.GetError() != "" {
		return ": " + s.GetError()
	}
	return ""
}
