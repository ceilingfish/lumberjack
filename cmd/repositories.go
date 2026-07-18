package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

// verbSetLogin is the `repositories NAME set-login` sub-verb.
const verbSetLogin = "set-login"

func newRepositoriesCmd() *cobra.Command {
	var (
		sync  bool
		force bool
	)

	c := &cobra.Command{
		Use:   "repositories [NAME [sync | worktrees | worktree BRANCH_OR_DIR delete | set-login [LOGIN]]]",
		Short: "List repositories, or show/sync one, its worktrees, set its login, or delete a worktree",
		Long: "Without arguments, lists every repository Lumberjack is tracking. With " +
			"--sync, first triggers a synchronisation of all repositories and streams " +
			"progress.\n\n" +
			"With a NAME, shows the last-sync detail for that repository:\n\n" +
			"  repositories NAME sync                          synchronise just that repository\n" +
			"  repositories NAME worktrees                     list worktrees\n" +
			"  repositories NAME worktree BRANCH_OR_DIR delete delete a worktree\n" +
			"  repositories NAME set-login [LOGIN]             set the gh account the repo uses (picker if omitted)\n\n" +
			"NAME resolves against a repository's name or local path. A delete " +
			"that would lose local commits asks for confirmation unless --force " +
			"is given.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				if len(args) == 0 {
					if sync {
						// Empty ref syncs every tracked repository.
						return runSync(ctx, cmd, cl, "")
					}
					repos, err := cl.ListRepositories(ctx)
					if err != nil {
						return err
					}
					return renderRepositories(cmd.OutOrStdout(), repos)
				}
				return dispatchRepository(ctx, cmd, cl, args, force)
			})
		},
	}

	c.Flags().BoolVar(&sync, "sync", false, "synchronise all repositories, streaming progress")
	c.Flags().BoolVar(&force, "force", false, "skip the confirmation prompt when deleting a worktree")
	c.ValidArgsFunction = completeRepositories
	return c
}

// completeRepositories drives positional completion for `repositories ...`,
// mirroring the argument grammar dispatchRepository routes on: a repository
// NAME first, then the sub-verb, then the login for `set-login` or `delete`
// after a worktree name.
func completeRepositories(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	switch {
	case len(args) == 0:
		return completeRepositoryNames(cmd), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1:
		return []string{"sync", "worktrees", "worktree", verbSetLogin}, cobra.ShellCompDirectiveNoFileComp
	case len(args) == 2 && args[1] == verbSetLogin:
		return completeLogins(cmd, args[0]), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 3 && args[1] == "worktree":
		return []string{"delete"}, cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// dispatchRepository routes `repositories NAME ...` to detail, worktree listing,
// or worktree deletion based on the positional arguments.
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

	case len(rest) == 1 && rest[0] == "sync":
		return runSync(ctx, cmd, cl, name)

	case len(rest) == 1 && rest[0] == "worktrees":
		wts, err := cl.ListWorktrees(ctx, name)
		if err != nil {
			return err
		}
		return renderWorktrees(cmd.OutOrStdout(), wts)

	case len(rest) == 3 && rest[0] == "worktree" && rest[2] == "delete":
		return deleteWorktree(ctx, cmd, cl, name, rest[1], force)

	case len(rest) == 1 && rest[0] == verbSetLogin:
		// No LOGIN given — setLogin offers an interactive picker.
		return setLogin(ctx, cmd, cl, name, "")

	case len(rest) == 2 && rest[0] == verbSetLogin:
		return setLogin(ctx, cmd, cl, name, rest[1])

	default:
		return fmt.Errorf("unrecognised repositories command: %q\nsee `lumberjack repositories --help`", strings.Join(args, " "))
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
