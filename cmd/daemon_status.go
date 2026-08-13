package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/ceilingfish/lumberjack/internal/daemon"
	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// newDaemonStatusCmd reports whether the daemon is installed and running.
func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether the daemon is installed and running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			svc, err := newLifecycle("", "")
			if err != nil {
				return err
			}
			return reportStatus(cmd.OutOrStdout(), svc, format)
		},
	}
}

// reportStatus prints the daemon's state. The service manager is the source of
// truth for state; the pid file adds the concrete live process id when up. A
// not-installed daemon is reported as such and returns errNotInstalled so the
// command exits non-zero for scripts.
func reportStatus(out io.Writer, svc lifecycle, format present.Format) error {
	status, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			if werr := emitDaemonMessage(out, format, "lumberjack daemon: not installed"); werr != nil {
				return werr
			}
			return errNotInstalled
		}
		return fmt.Errorf("checking daemon status: %w", err)
	}

	var msg string
	switch status {
	case service.StatusRunning:
		if pid, alive, perr := daemon.ReadPID(); perr == nil && alive {
			msg = fmt.Sprintf("lumberjack daemon: running (pid %d)", pid)
		} else {
			msg = "lumberjack daemon: running"
		}
	case service.StatusStopped:
		msg = "lumberjack daemon: installed, stopped"
	default:
		msg = "lumberjack daemon: installed, status unknown"
	}
	return emitDaemonMessage(out, format, msg)
}
