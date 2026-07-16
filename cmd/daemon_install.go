package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newDaemonInstallCmd registers the daemon with the platform service manager so
// it starts automatically at login. On macOS this writes a LaunchAgent plist to
// ~/Library/LaunchAgents; no sudo is required for a per-user agent.
func newDaemonInstallCmd() *cobra.Command {
	var socketPath string

	c := &cobra.Command{
		Use:   "install",
		Short: "Install the daemon to start automatically at login",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := newService(socketPath)
			if err != nil {
				return err
			}
			return installDaemon(cmd.OutOrStdout(), svc)
		},
	}
	c.Flags().StringVar(&socketPath, "socket", "",
		"Unix socket path baked into the installed service (default: ~/.lumberjack/daemon.sock)")
	return c
}

// installDaemon registers svc with the service manager and reports next steps.
func installDaemon(out io.Writer, svc lifecycle) error {
	if err := svc.Install(); err != nil {
		return fmt.Errorf("installing daemon: %w", err)
	}
	_, err := fmt.Fprintln(out,
		"lumberjack daemon installed; it will start at login. Run `lumberjack daemon start` to start it now.")
	return err
}
