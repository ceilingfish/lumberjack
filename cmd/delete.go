package cmd

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "delete NAME",
		Short: "Stop tracking a repository, removing it and its worktrees from the database",
		Long: "Removes the repository resolved by NAME and all of its worktrees from " +
			"Lumberjack's database. Nothing is deleted on disk or on GitHub — this only " +
			"stops Lumberjack tracking the repository. Re-add it later with `lumberjack init`.\n\n" +
			"NAME resolves against a repository's name or local path.",
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeRepositoryNames(cmd), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, cl *client.Client) error {
				resp, err := cl.DeleteRepository(ctx, args[0])
				if err != nil {
					return err
				}
				if format == present.JSON {
					return present.WriteJSONObject(cmd.OutOrStdout(), resp)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), resp.GetMessage())
				return err
			})
		},
	}
	return c
}
