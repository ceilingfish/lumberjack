package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/cli"
	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

type setRepositoryResult struct {
	Message string `json:"message"`
}

func newSetRepositoryCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "set-repository OWNER/NAME",
		Short: "Point a tracked checkout at a different GitHub repository",
		Long: "Changes the GitHub repository (OWNER/NAME) the tracked checkout at " +
			"the current working directory, or the one named by --repository, " +
			"syncs pull requests from. Every tracked pull request is forgotten and " +
			"re-read from the new repository on the next sync; local worktrees " +
			"stay tracked and are re-linked to pull requests by branch.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, ok := strings.Cut(args[0], "/")
			if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
				return fmt.Errorf("repository must be given as OWNER/NAME, got %q", args[0])
			}
			ref, err := cli.ResolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			return cli.WithClient(cmd, func(ctx context.Context, c *client.Client) error {
				repo, err := c.SetRepository(ctx, ref, owner, name)
				if err != nil {
					return err
				}
				msg := fmt.Sprintf("Set repository for %s to %s/%s", repo.GetDirPrefix(), repo.GetGithubOwner(), repo.GetGithubName())
				if format == present.JSON {
					return present.WriteJSONObject(cmd.OutOrStdout(), setRepositoryResult{Message: msg})
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
				return err
			})
		},
	}

	cli.AddRepositoryFlag(c, &repository)
	return c
}
