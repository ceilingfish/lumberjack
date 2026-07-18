package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ceilingfish/lumberjack/pkg/client"
	"github.com/spf13/cobra"
)

// withClient dials the daemon, runs fn with a connected client bound to the
// command's context, and closes the connection afterwards. Every CLI command
// that talks to the daemon goes through here so dialing and cleanup live in
// one place (AGENTS.md: cmd files stay thin, delegating to pkg/client).
func withClient(cmd *cobra.Command, fn func(context.Context, *client.Client) error) error {
	c, err := client.Dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return fn(cmd.Context(), c)
}

// cwdAbs resolves the absolute path of the current working directory. Commands
// that operate on "the repository here" (sync, set-login, status) pass it as the
// repository ref, which the daemon resolves against a repo's local path.
func cwdAbs() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolving current directory: %w", err)
	}
	return filepath.Abs(cwd)
}
