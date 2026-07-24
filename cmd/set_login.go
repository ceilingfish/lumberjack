package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

// setLoginResult is the json Format view model for `set-login`: SetLogin's
// response is just the updated Repository, which doesn't carry the
// human-readable confirmation message this command prints.
type setLoginResult struct {
	Message string `json:"message"`
}

func newSetLoginCmd() *cobra.Command {
	var repository string

	c := &cobra.Command{
		Use:   "set-login [LOGIN]",
		Short: "Set the gh account for a repository",
		Long: "Sets the gh account (`gh auth switch --user LOGIN`) the daemon " +
			"operates the repository under: the tracked repository at the " +
			"current working directory, or the repository named by " +
			"--repository. LOGIN must be an account gh is signed in as; omit it " +
			"to pick from the authenticated accounts interactively.",
		Args: cobra.MaximumNArgs(1),
		// The optional LOGIN completes to the gh accounts authenticated for the
		// resolved repository.
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeLogins(cmd, ref), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := resolveRepositoryRef(repository)
			if err != nil {
				return err
			}
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			login := ""
			if len(args) == 1 {
				login = args[0]
			}
			return withClient(cmd, func(ctx context.Context, c *client.Client) error {
				return setLogin(ctx, cmd, c, ref, login, format)
			})
		},
	}

	addRepositoryFlag(c, &repository)
	return c
}

// setLogin sets the gh login for the repository resolved by ref and reports the
// result. ref is either the current working directory (no --repository) or the
// value of --repository, per resolveRepositoryRef.
//
// An empty login means the user did not name one: the daemon reports the
// accounts gh is authenticated as for the repo's host and the user picks one
// interactively. The picker itself is unaffected by format — it writes to
// stderr, not stdout — so it runs the same way under every format.
func setLogin(ctx context.Context, cmd *cobra.Command, cl *client.Client, ref, login string, format present.Format) error {
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
	if format == present.JSON {
		return present.WriteJSONObject(cmd.OutOrStdout(),
			setLoginResult{Message: fmt.Sprintf("Set login for %s to %s", repo.GetDirPrefix(), repo.GetLogin())})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"Set login for %s to %s\n", repo.GetDirPrefix(), repo.GetLogin())
	return err
}
