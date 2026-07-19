package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "sync",
		Short: "Synchronise worktrees for a repository",
		Long: "Reconciles worktrees for the tracked repository at the current " +
			"working directory, or for the repository named by --repository, " +
			"against its open PRs. To sync every tracked repository, use " +
			"`lumberjack sync-all`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				return runSync(ctx, cmd, c, ref)
			})
		},
	}

	addRepositoryFlag(c, &repository)
	return c
}

func newSyncAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync-all",
		Short: "Synchronise worktrees for every tracked repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				// Empty ref syncs every tracked repository.
				return runSync(ctx, cmd, c, "")
			})
		},
	}
}

// runSync streams a sync of ref (empty = all repositories) and renders, per
// repository, a branch/PR/action table of the changes followed by a summary
// line. Shared by `sync` and `sync-all`.
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
			repo, summaryStatus(s),
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

func summaryError(s *lumberjackv1.SyncSummary) string {
	if s.GetError() != "" {
		return ": " + s.GetError()
	}
	return ""
}
