// Package daemon is Lumberjack's server: the gRPC service, the domain Service
// that owns all worktree/init/sync logic, and the hourly background sync loop.
// It is the sole owner of the database and the working trees.
package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// Info carries build/runtime metadata the daemon reports over the wire. The
// cobra command supplies it (see cmd/daemon.go) so the version is stamped at
// build time rather than hard-coded here.
type Info struct {
	Version   string
	StartedAt time.Time
}

// Config is the daemon's runtime configuration. Kept deliberately small; grows
// into internal/config when more settings appear (AGENTS.md: "start flat").
type Config struct {
	// SocketPath is the Unix domain socket the daemon listens on. Empty means
	// resolve the default (LUMBERJACK_SOCKET_PATH or ~/.lumberjack/daemon.sock).
	SocketPath string
}

// Module is the daemon's fx module: it wires every gRPC service into the
// server and runs the listener under fx's lifecycle. Import it from the
// `daemon` cobra command and supply Config + Info.
var Module = fx.Module("daemon",
	fx.Provide(
		// Domain dependencies. git and gh are provided as the GitOps/GHOps
		// interfaces the Service depends on, so tests can substitute fakes.
		newDatabase,
		fx.Annotate(worktree.NewGit, fx.As(new(GitOps))),
		fx.Annotate(github.NewClient, fx.As(new(GHOps))),
		NewService,

		NewServer,
		newGRPCServer,
	),
	fx.Invoke(runServer),
	fx.Invoke(runSyncLoop),
	fx.Invoke(writePIDFile),
)

// newDatabase opens the SQLite database (running migrations) and registers a
// lifecycle hook to close it on shutdown. The path honours LUMBERJACK_DB_PATH.
func newDatabase(lc fx.Lifecycle) (*database.Client, error) {
	path, err := database.DefaultPath()
	if err != nil {
		return nil, err
	}
	db, err := database.Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error { return db.Close() },
	})
	return db, nil
}

// newGRPCServer builds the server and binds the LumberjackService onto it.
// Server-wide concerns (reflection) live here.
func newGRPCServer(srv *Server) *grpc.Server {
	s := grpc.NewServer()
	srv.RegisterGRPC(s)
	// Reflection lets grpcurl and the like introspect the daemon over the
	// socket — cheap and useful for a local dev daemon.
	reflection.Register(s)
	return s
}

// runServer binds the socket and serves under fx's lifecycle: Serve runs in a
// goroutine on start, GracefulStop drains it on stop. A Serve error triggers
// app shutdown so the process exits non-zero instead of hanging.
func runServer(lc fx.Lifecycle, srv *grpc.Server, cfg Config, sd fx.Shutdowner) error {
	path, err := resolveSocketPath(cfg.SocketPath)
	if err != nil {
		return err
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := listen(path)
			if err != nil {
				return err
			}
			go func() {
				if serveErr := srv.Serve(ln); serveErr != nil {
					_ = sd.Shutdown(fx.ExitCode(1))
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			srv.GracefulStop()
			// Closing the Unix listener already unlinks the socket, so a
			// "not exist" here is the normal case, not an error.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		},
	})
	return nil
}

// listen creates the Unix domain socket, removing any stale socket left by a
// previous crash so a restart isn't blocked by "address already in use".
func listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating socket dir: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	return ln, nil
}

// resolveSocketPath applies the precedence: explicit Config >
// LUMBERJACK_SOCKET_PATH > ~/.lumberjack/daemon.sock.
func resolveSocketPath(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if env := os.Getenv("LUMBERJACK_SOCKET_PATH"); env != "" {
		return env, nil
	}
	dir, err := lumberjackDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.sock"), nil
}

// lumberjackDir is Lumberjack's per-user state directory (~/.lumberjack). It is
// the single place the daemon keeps its socket and pid file so client and
// server agree on locations.
func lumberjackDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".lumberjack"), nil
}
