package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// newDaemonInstallCmd registers the daemon with the platform service manager so
// it starts automatically at login. On macOS this writes a LaunchAgent plist to
// ~/Library/LaunchAgents; no sudo is required for a per-user agent.
func newDaemonInstallCmd() *cobra.Command {
	var socketPath string
	var force bool

	c := &cobra.Command{
		Use:   "install",
		Short: "Install the daemon to start automatically at login",
		Long: "Register the daemon with the platform service manager so it starts " +
			"at login. On macOS this writes a LaunchAgent plist to " +
			"~/Library/LaunchAgents; no sudo is required for a per-user agent.\n\n" +
			"If the service is already installed, install fails so an existing " +
			"registration is never silently clobbered. Pass --force to upgrade an " +
			"existing install in place: the old service is stopped and removed, then " +
			"reinstalled with the current binary path, socket, and environment.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := newService(socketPath)
			if err != nil {
				return err
			}
			return installDaemon(cmd.OutOrStdout(), svc, force)
		},
	}
	c.Flags().StringVar(&socketPath, "socket", "",
		"Unix socket path baked into the installed service (default: ~/.lumberjack/daemon.sock)")
	c.Flags().BoolVar(&force, "force", false,
		"Reinstall over an existing install (upgrade the registered binary and environment)")
	return c
}

// installDaemon registers svc with the service manager and reports next steps.
// When force is set, an existing install is removed first so the registration is
// refreshed with the current binary path and environment — the upgrade path.
func installDaemon(out io.Writer, svc lifecycle, force bool) error {
	if force {
		if err := reinstallDaemon(svc); err != nil {
			return err
		}
	} else if err := svc.Install(); err != nil {
		return fmt.Errorf("installing daemon: %w", err)
	}
	_, err := fmt.Fprintln(out,
		"lumberjack daemon installed; it will start at login. Run `lumberjack daemon start` to start it now.")
	return err
}

// reinstallDaemon removes any existing registration, then installs fresh. The
// service is stopped before removal so an upgrade never leaves an orphaned
// process holding the socket. Missing pieces (not installed, not running) are
// not errors — the goal state is a fresh install regardless of the starting one.
func reinstallDaemon(svc lifecycle) error {
	// Stop is best-effort: a not-running service errors here on most platforms,
	// and Uninstall unloads it anyway. What matters is that Install succeeds.
	_ = svc.Stop()
	if err := svc.Uninstall(); err != nil && !errors.Is(err, service.ErrNotInstalled) {
		return fmt.Errorf("removing existing daemon for reinstall: %w", err)
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("installing daemon: %w", err)
	}
	return nil
}
