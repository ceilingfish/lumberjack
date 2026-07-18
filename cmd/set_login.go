package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

func newSetLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-login [LOGIN]",
		Short: "Set the gh account for the repository in the current directory",
		Long: "Sets the gh account (`gh auth switch --user LOGIN`) the daemon " +
			"operates the current directory's tracked repository under. Run it " +
			"from the repo's main checkout. LOGIN must be an account gh is signed " +
			"in as; omit it to pick from the authenticated accounts interactively. " +
			"To target a repository by name from anywhere, use " +
			"`lumberjack repositories NAME set-login [LOGIN]`.",
		Args: cobra.MaximumNArgs(1),
		// The optional LOGIN completes to the gh accounts authenticated for the
		// current directory's repository.
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			ref, err := cwdAbs()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeLogins(cmd, ref), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := cwdAbs()
			if err != nil {
				return err
			}
			login := ""
			if len(args) == 1 {
				login = args[0]
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				return setLogin(ctx, cmd, c, abs, login)
			})
		},
	}
}

// setLogin sets the gh login for the repository resolved by ref and reports the
// result. Shared by the top-level `set-login` command (ref = current directory)
// and `repositories NAME set-login [LOGIN]` (ref = NAME).
//
// An empty login means the user did not name one: the daemon reports the
// accounts gh is authenticated as for the repo's host and the user picks one
// interactively.
func setLogin(ctx context.Context, cmd *cobra.Command, cl *client.Client, ref, login string) error {
	if login == "" {
		logins, current, err := cl.ListLogins(ctx, ref)
		if err != nil {
			return err
		}
		if len(logins) == 0 {
			return errors.New("gh has no authenticated accounts for this repository's host; run `gh auth login` first")
		}
		login, err = loginPicker(cmd, logins, current)
		if err != nil {
			return err
		}
	}
	repo, err := cl.SetLogin(ctx, ref, login)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"Set login for %s to %s\n", repo.GetDirPrefix(), repo.GetLogin())
	return err
}
