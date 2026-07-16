package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// newDaemonStopCmd stops the daemon if it is running.
func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon if it is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := newService("")
			if err != nil {
				return err
			}
			return stopDaemon(cmd.OutOrStdout(), svc)
		},
	}
}

// stopDaemon checks status first so stopping an already-stopped daemon reports
// clearly instead of surfacing a manager error, then stops the daemon.
func stopDaemon(out io.Writer, svc lifecycle) error {
	status, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return errNotInstalled
		}
		return fmt.Errorf("checking daemon status: %w", err)
	}
	if status != service.StatusRunning {
		_, err = fmt.Fprintln(out, "lumberjack daemon is not running.")
		return err
	}
	if err := svc.Stop(); err != nil {
		return fmt.Errorf("stopping daemon: %w", err)
	}
	_, err = fmt.Fprintln(out, "lumberjack daemon stopped.")
	return err
}
