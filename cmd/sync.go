package cmd

import (
	"context"
	"fmt"

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

// runSync streams a sync of ref (empty = all repositories) and renders the
// progress lines and per-repository summaries. Shared by `sync` and
// `repositories --sync`.
func runSync(ctx context.Context, cmd *cobra.Command, c *client.Client, ref string) error {
	out := cmd.OutOrStdout()
	return c.Sync(ctx, ref, func(e *lumberjackv1.SyncResponse) error {
		if !e.GetCompleted() {
			_, err := fmt.Fprintf(out, "%s: %s\n", e.GetRepository(), e.GetMessage())
			return err
		}
		s := e.GetSummary()
		_, err := fmt.Fprintf(out, "%s: %s (+%d worktree(s), -%d)%s\n",
			e.GetRepository(), summaryStatus(s),
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
