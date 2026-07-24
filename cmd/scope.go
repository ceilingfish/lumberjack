package cmd

import "github.com/spf13/cobra"

// repositoryFlagName is the uniform scope flag every per-repository command
// (status, sync, worktrees, worktree delete, set-login) exposes.
const repositoryFlagName = "repository"

// addRepositoryFlag registers --repository on c, binding it to p. Absent, a
// scoped command targets the tracked repository at the current working
// directory; present, it targets the named repository.
func addRepositoryFlag(c *cobra.Command, p *string) {
	c.Flags().StringVar(p, repositoryFlagName, "", "target this repository instead of the one in the current directory")
	_ = c.RegisterFlagCompletionFunc(repositoryFlagName, func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return completeRepositoryNames(cmd), cobra.ShellCompDirectiveNoFileComp
	})
}

// resolveRepositoryRef is the single place the uniform --repository flag
// falls back to the current working directory, so every scoped command
// behaves identically: repository, when given, is the ref to use; otherwise
// the ref is the CWD's absolute path (which the daemon resolves against a
// tracked repository, or reports "not found" for).
func resolveRepositoryRef(repository string) (string, error) {
	if repository != "" {
		return repository, nil
	}
	return cwdAbs()
}
