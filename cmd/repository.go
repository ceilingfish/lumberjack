package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newRepositoryCmd() *cobra.Command {
	var force bool

	c := &cobra.Command{
		Use:   "repository NAME [worktrees | worktree BRANCH_OR_DIR delete]",
		Short: "Show a repository, its worktrees, or delete a worktree",
		Long: "Without a subcommand, shows the last-sync detail for NAME.\n\n" +
			"  repository NAME worktrees                     list worktrees\n" +
			"  repository NAME worktree BRANCH_OR_DIR delete delete a worktree\n\n" +
			"NAME resolves against a repository's name or local path. A delete " +
			"that would lose local commits asks for confirmation unless --force " +
			"is given.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				return dispatchRepository(ctx, cmd, cl, args, force)
			})
		},
	}

	c.Flags().BoolVar(&force, "force", false, "skip the confirmation prompt when deleting a worktree")
	return c
}

// dispatchRepository routes `repository ...` to detail, worktree listing, or
// worktree deletion based on the positional arguments.
func dispatchRepository(ctx context.Context, cmd *cobra.Command, cl *client.Client, args []string, force bool) error {
	name := args[0]
	rest := args[1:]

	switch {
	case len(rest) == 0:
		repo, err := cl.GetRepository(ctx, name)
		if err != nil {
			return err
		}
		return renderRepositoryDetail(cmd.OutOrStdout(), repo)

	case len(rest) == 1 && rest[0] == "worktrees":
		wts, err := cl.ListWorktrees(ctx, name)
		if err != nil {
			return err
		}
		return renderWorktrees(cmd.OutOrStdout(), wts)

	case len(rest) == 3 && rest[0] == "worktree" && rest[2] == "delete":
		return deleteWorktree(ctx, cmd, cl, name, rest[1], force)

	default:
		return fmt.Errorf("unrecognised repository command: %q\nsee `lumberjack repository --help`", strings.Join(args, " "))
	}
}

// deleteWorktree performs the delete, handling the confirmation flow: an
// unforced delete that would lose work returns a warning, which the CLI shows
// before prompting and retrying with force.
func deleteWorktree(ctx context.Context, cmd *cobra.Command, cl *client.Client, name, worktree string, force bool) error {
	out := cmd.OutOrStdout()
	resp, err := cl.DeleteWorktree(ctx, name, worktree, force)
	if err != nil {
		return err
	}
	if resp.GetDeleted() {
		_, err := fmt.Fprintln(out, resp.GetMessage())
		return err
	}
	if !resp.GetRequiresConfirmation() {
		// Not deleted and no confirmation needed — surface any message.
		_, err := fmt.Fprintln(out, resp.GetMessage())
		return err
	}

	if _, err := fmt.Fprintf(out, "Warning: %s\n", resp.GetMessage()); err != nil {
		return err
	}
	if !confirm(cmd, fmt.Sprintf("Delete %s and lose %d commit(s)?", worktree, resp.GetCommitsAtRisk())) {
		_, err := fmt.Fprintln(out, "Aborted.")
		return err
	}

	resp, err = cl.DeleteWorktree(ctx, name, worktree, true)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, resp.GetMessage())
	return err
}

// confirm prompts for a yes/no answer on the command's input, defaulting to no.
func confirm(cmd *cobra.Command, prompt string) bool {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", prompt)
	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}
