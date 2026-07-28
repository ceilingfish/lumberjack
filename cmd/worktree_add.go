package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func newWorktreeAddCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "add BRANCH",
		Short: "Create a worktree for a branch",
		Long: "Creates a worktree for BRANCH in the conventional location for the " +
			"tracked repository at the current working directory, or the " +
			"repository named by --repository, then runs the repository's setup " +
			"steps against it. BRANCH is checked out from the remote if it exists " +
			"there, from an existing local branch if not, and otherwise created " +
			"off the default branch.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				resp, err := cl.AddWorktree(ctx, ref, args[0])
				if err != nil {
					return err
				}
				return emitAddedWorktree(cmd, format, resp)
			})
		},
	}

	addRepositoryFlag(c, &repository)
	return c
}

// emitAddedWorktree reports where the worktree landed and, when setup steps
// failed, warns about it — the worktree is kept either way, so this is a notice
// rather than an error.
func emitAddedWorktree(cmd *cobra.Command, format present.Format, resp *lumberjackv1.AddWorktreeResponse) error {
	out := cmd.OutOrStdout()
	if format == present.JSON {
		return present.WriteJSONObject(out, resp)
	}
	color := format == present.Color
	verb := "Checked out"
	if resp.GetBranchCreated() {
		verb = "Created branch"
	}
	if _, err := fmt.Fprintf(out, "%s %s at %s\n", verb,
		present.Branch(resp.GetBranch(), color), present.Path(resp.GetDirectoryPath(), color)); err != nil {
		return err
	}
	if e := resp.GetSetupError(); e != "" {
		_, err := fmt.Fprintf(out, "%s\n", present.StatusWarn("⚠ setup failed: "+e, color))
		return err
	}
	return nil
}
