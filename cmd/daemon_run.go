package cmd

import "github.com/spf13/cobra"

// newDaemonRunCmd runs the daemon in the foreground. It blocks until the process
// is signalled (SIGINT/SIGTERM) and is also the entry point the service manager
// invokes when the installed agent launches: service.Run adapts to the
// interactive vs service-managed context automatically.
func newDaemonRunCmd() *cobra.Command {
	var socketPath string

	c := &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, err := newService(socketPath, "")
			if err != nil {
				return err
			}
			return svc.Run()
		},
	}
	c.Flags().StringVar(&socketPath, "socket", "",
		"Unix socket path (default: $LUMBERJACK_SOCKET_PATH or ~/.lumberjack/daemon.sock)")
	return c
}
