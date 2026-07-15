package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
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

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Socket cleaned up on stop.
	if _, err := os.Stat(filepath.Join(dir, "daemon.sock")); !os.IsNotExist(err) {
		t.Errorf("socket not removed on stop: %v", err)
	}
}
