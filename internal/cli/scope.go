package cli

import "github.com/spf13/cobra"

// RepositoryFlagName is the uniform scope flag every per-repository command
// (status, sync, worktrees, worktree delete, set-login) exposes.
const RepositoryFlagName = "repository"

// AddRepositoryFlag registers --repository on c, binding it to p. Absent, a
// scoped command targets the tracked repository at the current working
// directory; present, it targets the named repository.
func AddRepositoryFlag(c *cobra.Command, p *string) {
	c.Flags().StringVar(p, RepositoryFlagName, "", "target this repository instead of the one in the current directory")
	_ = c.RegisterFlagCompletionFunc(RepositoryFlagName, func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return CompleteRepositoryNames(cmd), cobra.ShellCompDirectiveNoFileComp
	})
}

// ResolveRepositoryRef is the single place the uniform --repository flag
// falls back to the current working directory, so every scoped command
// behaves identically: repository, when given, is the ref to use; otherwise
// the ref is the CWD's absolute path (which the daemon resolves against a
// tracked repository, or reports "not found" for).
func ResolveRepositoryRef(repository string) (string, error) {
	if repository != "" {
		return repository, nil
	}
	return CwdAbs()
}
