package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func TestResolveSocketPath(t *testing.T) {
	// Explicit config wins.
	if got, _ := resolveSocketPath("/explicit.sock"); got != "/explicit.sock" {
		t.Errorf("explicit = %q", got)
	}
	// Env var next.
	t.Setenv("LUMBERJACK_SOCKET_PATH", "/env.sock")
	if got, _ := resolveSocketPath(""); got != "/env.sock" {
		t.Errorf("env = %q", got)
	}
	// Home fallback.
	t.Setenv("LUMBERJACK_SOCKET_PATH", "")
	got, err := resolveSocketPath("")
	if err != nil {
		t.Fatalf("resolveSocketPath: %v", err)
	}
	if filepath.Base(got) != "daemon.sock" {
		t.Errorf("fallback = %q", got)
	}
}

func TestListenCleansStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	// A stale file where the socket should be must not block listen.
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close()
}

// TestDaemonStartStop boots the full fx app and shuts it down, exercising the
// module wiring, socket lifecycle, database open/close, and sync loop start.
// It depends on git and gh being present on the host.
func TestDaemonStartStop(t *testing.T) {
	// The dependency graph constructs a git and gh client, so both must exist.
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	t.Setenv("LUMBERJACK_DB_PATH", filepath.Join(dir, "db.sqlite"))
	// Isolate the pid file (resolved under $HOME/.lumberjack) into the temp dir
	// so the test neither touches nor depends on the real home directory.
	t.Setenv("HOME", dir)

	app := fx.New(
		fx.Supply(
			Config{SocketPath: filepath.Join(dir, "daemon.sock")},
			Info{Version: "test", StartedAt: time.Now()},
		),
		Module,
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("app wiring: %v", err)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The socket must exist while running.
	if _, err := os.Stat(filepath.Join(dir, "daemon.sock")); err != nil {
		t.Errorf("socket not created: %v", err)
	}
	// The pid file must record this process while running.
	if pid, running, err := ReadPID(); err != nil || !running || pid != os.Getpid() {
		t.Errorf("pid file while running: got (%d, %v, %v), want (%d, true, nil)",
			pid, running, err, os.Getpid())
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Socket cleaned up on stop.
	if _, err := os.Stat(filepath.Join(dir, "daemon.sock")); !os.IsNotExist(err) {
		t.Errorf("socket not removed on stop: %v", err)
	}
	// Pid file cleaned up on stop.
	if _, running, err := ReadPID(); err != nil || running {
		t.Errorf("pid file after stop: got running=%v err=%v, want false/nil", running, err)
	}
}

type fakeShutdowner struct{ called chan struct{} }

func (f *fakeShutdowner) Shutdown(...fx.ShutdownOption) error {
	f.called <- struct{}{}
	return nil
}

func bindableSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lj")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "daemon.sock")
}

func TestResolveSocketPathFailsWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("LUMBERJACK_SOCKET_PATH", "")
	t.Setenv("HOME", "")
	if _, err := resolveSocketPath(""); err == nil {
		t.Error("expected an error with no home directory, got nil")
	}
}

func TestListenFailures(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(filepath.Join(blocked, "sub", "daemon.sock")); err == nil {
		t.Error("expected an error when the socket dir cannot be created")
	}

	occupied := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.MkdirAll(filepath.Join(occupied, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(occupied); err == nil {
		t.Error("expected an error when a stale socket cannot be removed")
	}

	if _, err := listen(filepath.Join(t.TempDir(), strings.Repeat("s", 120)+".sock")); err == nil {
		t.Error("expected an error binding an over-long socket path")
	}
}

func TestNewDatabaseFailures(t *testing.T) {
	t.Setenv("LUMBERJACK_DB_PATH", "")
	t.Setenv("HOME", "")
	if _, err := newDatabase(&recordingLifecycle{}); err == nil {
		t.Error("expected an error resolving the database path with no home directory")
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUMBERJACK_DB_PATH", filepath.Join(blocked, "db.sqlite"))
	if _, err := newDatabase(&recordingLifecycle{}); err == nil {
		t.Error("expected an error opening a database under a regular file")
	}
}

func TestNewDatabaseClosesOnStop(t *testing.T) {
	t.Setenv("LUMBERJACK_DB_PATH", filepath.Join(t.TempDir(), "db.sqlite"))
	lc := &recordingLifecycle{}
	db, err := newDatabase(lc)
	if err != nil {
		t.Fatalf("newDatabase: %v", err)
	}
	if err := lc.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	if _, err := db.ListRepositories(context.Background()); err == nil {
		t.Error("expected the database to be closed after OnStop")
	}
}

func TestRunServerFailsWithoutAResolvableSocketPath(t *testing.T) {
	t.Setenv("LUMBERJACK_SOCKET_PATH", "")
	t.Setenv("HOME", "")
	err := runServer(&recordingLifecycle{}, grpc.NewServer(), Config{}, &fakeShutdowner{})
	if err == nil {
		t.Error("expected an error with no resolvable socket path, got nil")
	}
}

func TestRunServerServesThenCleansUpTheSocket(t *testing.T) {
	path := bindableSocketPath(t)
	srv := grpc.NewServer()
	lc := &recordingLifecycle{}
	if err := runServer(lc, srv, Config{SocketPath: path}, &fakeShutdowner{called: make(chan struct{}, 1)}); err != nil {
		t.Fatalf("runServer: %v", err)
	}
	if err := lc.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("socket not created: %v", err)
	}
	if err := lc.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("socket not removed on stop: %v", err)
	}
}

func TestRunServerListenFailureFailsTheStart(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	lc := &recordingLifecycle{}
	cfg := Config{SocketPath: filepath.Join(blocked, "sub", "daemon.sock")}
	if err := runServer(lc, grpc.NewServer(), cfg, &fakeShutdowner{}); err != nil {
		t.Fatalf("runServer: %v", err)
	}
	if err := lc.hooks[0].OnStart(context.Background()); err == nil {
		t.Error("expected OnStart to fail when the socket cannot be bound")
	}
}

func TestRunServerServeFailureShutsTheAppDown(t *testing.T) {
	srv := grpc.NewServer()
	srv.Stop()
	sd := &fakeShutdowner{called: make(chan struct{}, 1)}
	lc := &recordingLifecycle{}
	cfg := Config{SocketPath: bindableSocketPath(t)}
	if err := runServer(lc, srv, cfg, sd); err != nil {
		t.Fatalf("runServer: %v", err)
	}
	if err := lc.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	select {
	case <-sd.called:
	case <-time.After(2 * time.Second):
		t.Fatal("a serve failure did not request shutdown")
	}
}

func TestRunServerStopSurfacesAnUnremovableSocket(t *testing.T) {
	occupied := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.MkdirAll(filepath.Join(occupied, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	lc := &recordingLifecycle{}
	if err := runServer(lc, grpc.NewServer(), Config{SocketPath: occupied}, &fakeShutdowner{}); err != nil {
		t.Fatalf("runServer: %v", err)
	}
	if err := lc.hooks[0].OnStop(context.Background()); err == nil {
		t.Error("expected OnStop to surface a socket it could not remove")
	}
}

func TestNewGRPCServerRegistersTheService(t *testing.T) {
	h := newHarness(t)
	s := newGRPCServer(newServer(h))
	defer s.Stop()
	if _, ok := s.GetServiceInfo()["lumberjack.v1.LumberjackService"]; !ok {
		t.Errorf("LumberjackService not registered: %v", s.GetServiceInfo())
	}
}
