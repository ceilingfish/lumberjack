package cmd

import (
	"github.com/spf13/cobra"
)

// newDaemonCmd is the `daemon` parent. It owns no behaviour itself; its
// subcommands manage the daemon's lifecycle (run/start/stop/status). Use the
// top-level `install`/`uninstall` commands to register or remove the daemon.
// Running `lumberjack daemon` with no subcommand prints help.
func newDaemonCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Lumberjack daemon (gRPC server + background sync)",
		Long: "The daemon is the long-running server that owns the database, drives " +
			"the hourly sync loop, and performs all worktree operations. These " +
			"subcommands run it and inspect or stop it. Use `lumberjack install` / " +
			"`lumberjack uninstall` to register or remove it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	c.AddCommand(newDaemonRunCmd())
	c.AddCommand(newDaemonStartCmd())
	c.AddCommand(newDaemonStopCmd())
	c.AddCommand(newDaemonStatusCmd())
	return c
}
