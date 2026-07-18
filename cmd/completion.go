package cmd

import (
	"context"
	"time"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

// completionTimeout bounds a shell-completion RPC. Completion runs on every
// <TAB>, so a slow or down daemon must fail fast rather than stall the shell.
// Login completion has the daemon shell out to `gh auth status`, which takes
// on the order of a second, so the budget accommodates that while still
// capping a hung daemon.
const completionTimeout = 2 * time.Second

// completionClient dials the daemon for a completion callback and runs fn with a
// short timeout. Completion must never hang the shell or print diagnostics, so
// any dial error yields no suggestions and the caller falls back to nothing.
func completionClient(cmd *cobra.Command, fn func(context.Context, *client.Client) []string) []string {
	cl, err := client.Dial()
	if err != nil {
		return nil
	}
	defer func() { _ = cl.Close() }()
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	return fn(ctx, cl)
}

// completeRepositoryNames suggests the names of every tracked repository,
// matching what `repositories NAME ...` resolves against.
func completeRepositoryNames(cmd *cobra.Command) []string {
	return completionClient(cmd, func(ctx context.Context, cl *client.Client) []string {
		repos, err := cl.ListRepositories(ctx)
		if err != nil {
			return nil
		}
		names := make([]string, 0, len(repos))
		for _, r := range repos {
			names = append(names, r.GetDirPrefix())
		}
		return names
	})
}

// completeLogins suggests the gh accounts authenticated for the host of the
// repository resolved by ref.
func completeLogins(cmd *cobra.Command, ref string) []string {
	return completionClient(cmd, func(ctx context.Context, cl *client.Client) []string {
		logins, _, err := cl.ListLogins(ctx, ref)
		if err != nil {
			return nil
		}
		return logins
	})
}
