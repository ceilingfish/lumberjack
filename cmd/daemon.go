package cmd

import (
	"time"

	"github.com/ceilingfish/lumberjack/internal/daemon"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func newDaemonCmd() *cobra.Command {
	var socketPath string

	c := &cobra.Command{
		Use:   "daemon",
		Short: "Run the Lumberjack daemon (gRPC server + background sync)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			app := fx.New(
				// Supply what the daemon module needs; fx wires the rest.
				fx.Supply(
					daemon.Config{SocketPath: socketPath},
					daemon.Info{Version: version, StartedAt: time.Now()},
				),
				daemon.Module,
				fx.NopLogger, // the daemon owns its own logging; silence fx's
			)
			if err := app.Err(); err != nil {
				return err // provide/invoke wiring failed — report it, don't run
			}
			// Run blocks until SIGINT/SIGTERM, then drives the fx stop hooks
			// (GracefulStop + socket cleanup).
			app.Run()
			return nil
		},
	}

	c.Flags().StringVar(&socketPath, "socket", "",
		"Unix socket path (default: $LUMBERJACK_SOCKET_PATH or ~/.lumberjack/daemon.sock)")
	return c
}
