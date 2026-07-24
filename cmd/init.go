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
		// The single positional is a repository checkout path; complete
		// directories only, and nothing once one is given.
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveFilterDirs
		},
		RunE: runInit,
	}
}

// runInit resolves the target path and registers it, reporting the tracking
// defaults and any worktrees adopted during registration, then prompts for
// setup-steps consent if the repository's trusted `.lumberjack.yml` declares
// run-command steps not yet consented to.
func runInit(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) == 1 {
		path = args[0]
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving path %q: %w", path, err)
	}
	return withClient(cmd, func(ctx context.Context, c *client.Client) error {
		repo, adopted, err := c.InitRepository(ctx, abs)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if _, err := fmt.Fprintf(out,
			"Tracking %s/%s at %s\nWorktrees will be created under %s\n",
			repo.GetGithubOwner(), repo.GetGithubName(),
			repo.GetLocalPath(), repo.GetWorktreeParentDir()); err != nil {
			return err
		}
		// A branch/PR/action table of the worktrees adopted during registration.
		if err := renderWorktreeChanges(out, adopted); err != nil {
			return err
		}
		return promptSetupConsent(ctx, cmd, c, repo.GetDirPrefix())
	})
}
