package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/daemon"
	"github.com/kardianos/service"
)

// fakeLifecycle is a controllable lifecycle for driving the install/start/stop/
// status logic without touching the real service manager. It records which
// control methods were called.
type fakeLifecycle struct {
	status       service.Status
	statusErr    error
	installErr   error
	uninstallErr error
	startErr     error
	stopErr      error

	installed   bool
	uninstalled bool
	started     bool
	stopped     bool
}

func (f *fakeLifecycle) Status() (service.Status, error) { return f.status, f.statusErr }
func (f *fakeLifecycle) Install() error                  { f.installed = true; return f.installErr }
func (f *fakeLifecycle) Uninstall() error               { f.uninstalled = true; return f.uninstallErr }
func (f *fakeLifecycle) Start() error                    { f.started = true; return f.startErr }
func (f *fakeLifecycle) Stop() error                     { f.stopped = true; return f.stopErr }

// TestNewService: the shared service handle builds for both the default and an
// explicit socket path, and reports a platform (proving kardianos wired up).
func TestNewService(t *testing.T) {
	for _, socket := range []string{"", "/tmp/lj.sock"} {
		svc, err := newService(socket)
		if err != nil {
			t.Fatalf("newService(%q): %v", socket, err)
		}
		if svc == nil {
			t.Fatalf("newService(%q) returned nil service", socket)
		}
		if svc.Platform() == "" {
			t.Errorf("newService(%q): empty platform", socket)
		}
	}
}

// TestCmdDaemonHelp: the bare `daemon` command prints help listing every
// lifecycle subcommand, and does not error.
func TestCmdDaemonHelp(t *testing.T) {
	out, err := run(t, "", "daemon")
	if err != nil {
		t.Fatalf("daemon: %v", err)
	}
	for _, sub := range []string{"run", "install", "start", "stop", "status"} {
		if !strings.Contains(out, sub) {
			t.Errorf("daemon help missing subcommand %q; out=%q", sub, out)
		}
	}
}

// TestCmdDaemonStatusEndToEnd: the real `daemon status` command is host-dependent
// (installed or not), so we only assert it produces a recognisable status line.
// HOME is isolated so it never reads a real pid file.
func TestCmdDaemonStatusEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _ := run(t, "", "daemon", "status")
	if !strings.Contains(out, "lumberjack daemon:") {
		t.Errorf("expected a status line, got %q", out)
	}
}

func TestInstallDaemon(t *testing.T) {
	var buf bytes.Buffer
	f := &fakeLifecycle{}
	if err := installDaemon(&buf, f, false); err != nil {
		t.Fatalf("installDaemon: %v", err)
	}
	if !f.installed {
		t.Error("Install was not called")
	}
	if f.uninstalled {
		t.Error("Uninstall should not be called without --force")
	}
	if !strings.Contains(buf.String(), "installed") {
		t.Errorf("out = %q", buf.String())
	}

	// Install failure is wrapped and surfaced.
	if err := installDaemon(&buf, &fakeLifecycle{installErr: errors.New("boom")}, false); err == nil {
		t.Error("expected install error")
	}
}

// TestCheckStableExecutable: installs from a `go run` temp build are refused
// with actionable guidance; durable paths pass.
func TestCheckStableExecutable(t *testing.T) {
	ephemeral := []string{
		"/var/folders/xb/T/go-build2170204171/b001/exe/lumberjack",             // temp work dir
		"/tmp/go-build123/b001/exe/lumberjack",                                 // temp work dir
		"/Users/tom/Library/Caches/go-build/42/42dd568…-d/lumberjack",          // persistent build cache
	}
	for _, p := range ephemeral {
		err := checkStableExecutable(p)
		if err == nil {
			t.Errorf("checkStableExecutable(%q) = nil, want error", p)
			continue
		}
		if !strings.Contains(err.Error(), "./bin/lumberjack") {
			t.Errorf("error for %q lacks durable-install guidance: %v", p, err)
		}
	}

	durable := []string{
		"/Users/tom.parrish/Code/ceilingfish/Lumberjack/bin/lumberjack",
		"/usr/local/bin/lumberjack",
		"/opt/homebrew/bin/lumberjack",
	}
	for _, p := range durable {
		if err := checkStableExecutable(p); err != nil {
			t.Errorf("checkStableExecutable(%q) = %v, want nil", p, err)
		}
	}
}

// TestInstallDaemonForce: --force removes any existing registration (Stop +
// Uninstall) before installing, so an "already exists" error can never occur.
func TestInstallDaemonForce(t *testing.T) {
	var buf bytes.Buffer
	f := &fakeLifecycle{status: service.StatusRunning}
	if err := installDaemon(&buf, f, true); err != nil {
		t.Fatalf("installDaemon force: %v", err)
	}
	if !f.uninstalled {
		t.Error("Uninstall was not called under --force")
	}
	if !f.installed {
		t.Error("Install was not called under --force")
	}

	// A not-installed starting state is not an error: force still installs.
	f = &fakeLifecycle{uninstallErr: service.ErrNotInstalled, stopErr: service.ErrNotInstalled}
	if err := installDaemon(&buf, f, true); err != nil {
		t.Fatalf("force over not-installed: %v", err)
	}
	if !f.installed {
		t.Error("Install was not called when nothing was previously installed")
	}

	// A real Uninstall failure aborts before reinstalling.
	f = &fakeLifecycle{uninstallErr: errors.New("boom")}
	if err := installDaemon(&buf, f, true); err == nil {
		t.Error("expected uninstall error to surface")
	} else if f.installed {
		t.Error("Install should not run after a failed Uninstall")
	}
}

func TestStartDaemon(t *testing.T) {
	tests := []struct {
		name       string
		f          *fakeLifecycle
		wantStart  bool
		wantErr    error
		wantOutput string
	}{
		{"stopped starts", &fakeLifecycle{status: service.StatusStopped}, true, nil, "started"},
		{"already running", &fakeLifecycle{status: service.StatusRunning}, false, nil, "already running"},
		{"not installed", &fakeLifecycle{statusErr: service.ErrNotInstalled}, false, errNotInstalled, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := startDaemon(&buf, tt.f)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.f.started != tt.wantStart {
				t.Errorf("started = %v, want %v", tt.f.started, tt.wantStart)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("out = %q, want to contain %q", buf.String(), tt.wantOutput)
			}
		})
	}
}

func TestStopDaemon(t *testing.T) {
	tests := []struct {
		name       string
		f          *fakeLifecycle
		wantStop   bool
		wantErr    error
		wantOutput string
	}{
		{"running stops", &fakeLifecycle{status: service.StatusRunning}, true, nil, "stopped"},
		{"already stopped", &fakeLifecycle{status: service.StatusStopped}, false, nil, "not running"},
		{"not installed", &fakeLifecycle{statusErr: service.ErrNotInstalled}, false, errNotInstalled, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := stopDaemon(&buf, tt.f)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.f.stopped != tt.wantStop {
				t.Errorf("stopped = %v, want %v", tt.f.stopped, tt.wantStop)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("out = %q, want to contain %q", buf.String(), tt.wantOutput)
			}
		})
	}
}

func TestReportStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the pid file read

	tests := []struct {
		name    string
		f       *fakeLifecycle
		wantErr error
		want    string
	}{
		{"running", &fakeLifecycle{status: service.StatusRunning}, nil, "running"},
		{"stopped", &fakeLifecycle{status: service.StatusStopped}, nil, "installed, stopped"},
		{"unknown", &fakeLifecycle{status: service.StatusUnknown}, nil, "status unknown"},
		{"not installed", &fakeLifecycle{statusErr: service.ErrNotInstalled}, errNotInstalled, "not installed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := reportStatus(&buf, tt.f)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Errorf("out = %q, want to contain %q", buf.String(), tt.want)
			}
		})
	}
}

// TestReportStatusWithPID: when running and a live pid file exists, the pid is
// reported.
func TestReportStatusWithPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := daemon.PIDFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := reportStatus(&buf, &fakeLifecycle{status: service.StatusRunning}); err != nil {
		t.Fatalf("reportStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("out = %q, want live pid", buf.String())
	}
}
