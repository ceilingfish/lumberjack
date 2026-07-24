package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// daemonMessage is the json Format view model for daemon lifecycle commands:
// they report a simple human-readable outcome with no proto type behind it.
type daemonMessage struct {
	Message string `json:"message"`
}

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
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			svc, err := newService("", "")
			if err != nil {
				return err
			}
			return startDaemon(cmd.OutOrStdout(), svc, format)
		},
	}
}

// startDaemon queries the service manager's status first so a second `start` is
// a friendly no-op rather than an error, then starts the daemon.
func startDaemon(out io.Writer, svc lifecycle, format present.Format) error {
	status, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return errNotInstalled
		}
		return fmt.Errorf("checking daemon status: %w", err)
	}
	if status == service.StatusRunning {
		return emitDaemonMessage(out, format, "lumberjack daemon is already running.")
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	return emitDaemonMessage(out, format, "lumberjack daemon started.")
}

// emitDaemonMessage writes msg per format: the bare JSON view model, or plain
// text (color and structured render identically — there is nothing to
// colourise in a one-line status message).
func emitDaemonMessage(out io.Writer, format present.Format, msg string) error {
	if format == present.JSON {
		return present.WriteJSONObject(out, daemonMessage{Message: msg})
	}
	_, err := fmt.Fprintln(out, msg)
	return err
}
