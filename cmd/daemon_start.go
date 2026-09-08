package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/ceilingfish/lumberjack/internal/cli"

	"github.com/ceilingfish/lumberjack/internal/daemon"
	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// newDaemonStartCmd starts the installed daemon if it is not already running.
func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon if it is not already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cli.OutputFormat(cmd)
			if err != nil {
				return err
			}
			svc, err := daemon.NewLifecycle("", "", cli.Version)
			if err != nil {
				return err
			}
			return startDaemon(cmd.OutOrStdout(), svc, format)
		},
	}
}

// startDaemon queries the service manager's status first so a second `start` is
// a friendly no-op rather than an error, then starts the daemon.
func startDaemon(out io.Writer, svc daemon.Lifecycle, format present.Format) error {
	status, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return daemon.ErrNotInstalled
		}
		return fmt.Errorf("checking daemon status: %w", err)
	}
	if status == service.StatusRunning {
		return present.WriteMessage(out, format, "lumberjack daemon is already running.")
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	return present.WriteMessage(out, format, "lumberjack daemon started.")
}
