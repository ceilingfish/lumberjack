package cmd

import (
	"context"
	"os"
	"time"

	"github.com/ceilingfish/lumberjack/internal/daemon"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

// serviceName is the identifier registered with the platform service manager
// (launchd on macOS). It also names the generated LaunchAgent plist.
const serviceName = "lumberjack"

// lifecycle is the slice of service.Service the install/start/stop/status
// commands actually use. Depending on this narrow interface (rather than the
// full service.Service) keeps the command logic in testable free functions
// that a fake can drive without touching the real service manager.
type lifecycle interface {
	Status() (service.Status, error)
	Start() error
	Stop() error
	Install() error
}

// newDaemonCmd is the `daemon` parent. It owns no behaviour itself; its
// subcommands manage the daemon's lifecycle (run/install/start/stop/status).
// Running `lumberjack daemon` with no subcommand prints help.
func newDaemonCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Lumberjack daemon (gRPC server + background sync)",
		Long: "The daemon is the long-running server that owns the database, drives " +
			"the hourly sync loop, and performs all worktree operations. These " +
			"subcommands run it, install it to start at login, and inspect or stop it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	c.AddCommand(newDaemonRunCmd())
	c.AddCommand(newDaemonInstallCmd())
	c.AddCommand(newDaemonStartCmd())
	c.AddCommand(newDaemonStopCmd())
	c.AddCommand(newDaemonStatusCmd())
	return c
}

// program implements service.Interface. kardianos calls Start (which must not
// block) then, on shutdown, Stop — in both the foreground (`daemon run`) and
// service-managed (launchd) contexts. We drive the fx app's lifecycle directly
// (Start/Stop) rather than app.Run(), since Run blocks and owns signal handling
// that the service manager provides for us.
type program struct {
	socketPath string
	app        *fx.App
}

// Start builds and starts the fx app without blocking.
func (p *program) Start(service.Service) error {
	p.app = fx.New(
		fx.Supply(
			daemon.Config{SocketPath: p.socketPath},
			daemon.Info{Version: version, StartedAt: time.Now()},
		),
		daemon.Module,
		fx.NopLogger, // the daemon owns its own logging; silence fx's
	)
	if err := p.app.Err(); err != nil {
		return err // provide/invoke wiring failed — report it, don't start
	}
	startCtx, cancel := context.WithTimeout(context.Background(), p.app.StartTimeout())
	defer cancel()
	return p.app.Start(startCtx)
}

// Stop drives the fx stop hooks (GracefulStop + socket/pid cleanup).
func (p *program) Stop(service.Service) error {
	if p.app == nil {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), p.app.StopTimeout())
	defer cancel()
	return p.app.Stop(stopCtx)
}

// newService builds the platform service handle shared by every daemon
// subcommand. It is installed as a per-user agent (LaunchAgent on macOS) so it
// runs as the invoking user — keeping ~/.lumberjack paths and the user's `gh`
// credentials valid — and starts at login. socketPath, when set, is threaded
// into both the running program and the arguments the manager launches with.
func newService(socketPath string) (service.Service, error) {
	args := []string{"daemon", "run"}
	if socketPath != "" {
		args = append(args, "--socket", socketPath)
	}
	cfg := &service.Config{
		Name:        serviceName,
		DisplayName: "Lumberjack Daemon",
		Description: "Tracks a GitHub repository's open PRs and reconciles git worktrees.",
		Arguments:   args,
		// launchd starts agents with a bare PATH (/usr/bin:/bin:/usr/sbin:/sbin),
		// which omits Homebrew and other user tool directories — so the daemon
		// can't find `gh` and crash-loops. Bake the install-time PATH (the user's
		// login shell PATH, which resolved `gh` when they installed) into the
		// service's environment so runtime lookups match install-time lookups.
		EnvVars: serviceEnv(),
		Option: service.KeyValue{
			"UserService": true, // ~/Library/LaunchAgents, runs as the user
			"RunAtLoad":   true, // start at login
			"KeepAlive":   true, // restart if it exits unexpectedly
		},
	}
	return service.New(&program{socketPath: socketPath}, cfg)
}

// serviceEnv is the environment baked into the installed service. It carries the
// install-time PATH forward so the daemon resolves `gh` (and git) under launchd's
// otherwise-minimal PATH. If PATH is somehow unset, we omit it and let the
// service manager supply its default rather than pinning an empty value.
func serviceEnv() map[string]string {
	env := map[string]string{}
	if path := os.Getenv("PATH"); path != "" {
		env["PATH"] = path
	}
	return env
}
