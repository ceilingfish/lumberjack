package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

// newWorktreeCmd is the `worktree` parent command. Kept distinct from the
// top-level `delete` (repository) command so the two "delete" meanings never
// collide: `delete NAME` untracks a repository, `worktree delete
// BRANCH_OR_DIR` removes one worktree.
func newWorktreeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "worktree",
		Short: "Manage a repository's worktrees",
	}
	c.AddCommand(newWorktreeDeleteCmd())
	return c
}

func newWorktreeDeleteCmd() *cobra.Command {
	var (
		repository string
		force      bool
	)

	c := &cobra.Command{
		Use:   "delete BRANCH_OR_DIR",
		Short: "Delete a worktree",
		Long: "Deletes the worktree matching BRANCH_OR_DIR (its branch name or " +
			"directory) from the tracked repository at the current working " +
			"directory, or the repository named by --repository. A delete that " +
			"would lose local commits asks for confirmation unless --force is " +
			"given.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				return deleteWorktree(ctx, cmd, cl, ref, args[0], force)
			})
		},
	}

	c.Flags().BoolVar(&force, "force", false, "skip the confirmation prompt when deleting a worktree")
	addRepositoryFlag(c, &repository)
	return c
}

// deleteWorktree performs the delete, handling the confirmation flow: an
// unforced delete that would lose work returns a warning, which the CLI shows
// before prompting and retrying with force.
func deleteWorktree(ctx context.Context, cmd *cobra.Command, cl *client.Client, ref, worktree string, force bool) error {
	out := cmd.OutOrStdout()
	resp, err := cl.DeleteWorktree(ctx, ref, worktree, force)
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

	resp, err = cl.DeleteWorktree(ctx, ref, worktree, true)
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
