package cmd

import (
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
	c.AddCommand(newWorktreeAddCmd())
	c.AddCommand(newWorktreeDeleteCmd())
	return c
}
