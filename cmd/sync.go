package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
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
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				return runSync(ctx, cmd, c, abs, format)
			})
		},
	}
}

// syncResult is the json Format view model for one repository's sync
// outcome: no single proto message carries a repository's changes alongside
// its summary (SyncResponse is one stream event at a time), so this composite
// aggregates them once the stream completes. The changes and summary fields
// keep their protojson rendering via json.RawMessage.
type syncResult struct {
	Repository string            `json:"repository"`
	Changes    []json.RawMessage `json:"changes"`
	Summary    json.RawMessage   `json:"summary"`
}

// runSync streams a sync of ref (empty = all repositories). Under color and
// structured it renders, per repository, a branch/PR/action table of the
// changes followed by a summary line as they complete. Under json, results
// are buffered and emitted once as a single bare array, since only valid JSON
// may reach stdout in that mode. Shared by `sync` and `repositories --sync`.
func runSync(ctx context.Context, cmd *cobra.Command, c *client.Client, ref string, format present.Format) error {
	out := cmd.OutOrStdout()
	// Buffer each repo's per-branch changes so the table can be column-aligned
	// and printed once the repo completes. Repos stream sequentially, but keying
	// by name keeps this correct regardless of interleaving.
	changes := map[string][]*lumberjackv1.WorktreeChange{}
	var results []syncResult

	err := c.Sync(ctx, ref, func(e *lumberjackv1.SyncResponse) error {
		repo := e.GetRepository()
		if !e.GetCompleted() {
			if ch := e.GetChange(); ch != nil {
				changes[repo] = append(changes[repo], ch)
			} else if msg := e.GetMessage(); msg != "" && format != present.JSON {
				if _, err := fmt.Fprintf(out, "%s: %s\n", repo, msg); err != nil {
					return err
				}
			}
			return nil
		}
		repoChanges := changes[repo]
		delete(changes, repo)
		s := e.GetSummary()

		if format == present.JSON {
			changeRaws := make([]json.RawMessage, len(repoChanges))
			for i, ch := range repoChanges {
				raw, err := present.ProtoRaw(ch)
				if err != nil {
					return err
				}
				changeRaws[i] = raw
			}
			summaryRaw, err := present.ProtoRaw(s)
			if err != nil {
				return err
			}
			results = append(results, syncResult{Repository: repo, Changes: changeRaws, Summary: summaryRaw})
			return nil
		}

		if err := renderWorktreeChanges(out, repoChanges, format == present.Color); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "%s: %s (+%d worktree(s), -%d)%s\n",
			repo, summaryStatus(s, format == present.Color),
			s.GetWorktreesCreated(), s.GetWorktreesRemoved(), summaryError(s, format == present.Color))
		return err
	})
	if err != nil {
		return err
	}

	if format == present.JSON {
		return present.WriteJSONArray(out, results)
	}
	return nil
}

func summaryStatus(s *lumberjackv1.SyncSummary, color bool) string {
	if s.GetStatus() == lumberjackv1.SyncStatus_SYNC_STATUS_ERROR {
		return present.StatusErr("error", color)
	}
	return present.StatusOK("synced", color)
}

func summaryError(s *lumberjackv1.SyncSummary, color bool) string {
	if s.GetError() != "" {
		return ": " + present.StatusErr(s.GetError(), color)
	}
	return ""
}
