package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// errNotInstalled is the actionable message shown when a lifecycle command is
// run before `install`. kardianos returns service.ErrNotInstalled from
// Status/Start/Stop in that case.
var errNotInstalled = errors.New("daemon is not installed — run `lumberjack install` first")

// newDaemonStartCmd starts the installed daemon if it is not already running.
func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon if it is not already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := newService("", "")
			if err != nil {
				return err
			}
			return startDaemon(cmd.OutOrStdout(), svc)
		},
	}
}

// startDaemon queries the service manager's status first so a second `start` is
// a friendly no-op rather than an error, then starts the daemon.
func startDaemon(out io.Writer, svc lifecycle) error {
	status, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return errNotInstalled
		}
		return fmt.Errorf("checking daemon status: %w", err)
	}
	if status == service.StatusRunning {
		_, err = fmt.Fprintln(out, "lumberjack daemon is already running.")
		return err
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	_, err = fmt.Fprintln(out, "lumberjack daemon started.")
	return err
}
