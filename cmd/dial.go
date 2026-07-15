package cmd

import (
	"context"

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
