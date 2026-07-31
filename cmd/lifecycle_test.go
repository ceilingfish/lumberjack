package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/kardianos/service"
	"go.uber.org/fx"
)

func fakeServiceManager(t *testing.T, f *fakeLifecycle) {
	t.Helper()
	prev := newLifecycle
	newLifecycle = func(string, string) (lifecycle, error) { return f, nil }
	t.Cleanup(func() { newLifecycle = prev })
}

func unavailableServiceManager(t *testing.T, err error) {
	t.Helper()
	prev := newLifecycle
	newLifecycle = func(string, string) (lifecycle, error) { return nil, err }
	t.Cleanup(func() { newLifecycle = prev })
}

func installedCLI(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, cliBinaryName), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir
}

func TestCmdDaemonStart(t *testing.T) {
	f := &fakeLifecycle{status: service.StatusStopped}
	fakeServiceManager(t, f)

	out, err := run(t, "", "daemon", "start")
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	if !f.started {
		t.Error("daemon start did not start the service")
	}
	if !strings.Contains(out, "started") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdDaemonStop(t *testing.T) {
	f := &fakeLifecycle{status: service.StatusRunning}
	fakeServiceManager(t, f)

	out, err := run(t, "", "daemon", "stop")
	if err != nil {
		t.Fatalf("daemon stop: %v", err)
	}
	if !f.stopped {
		t.Error("daemon stop did not stop the service")
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdDaemonStatusReportsTheManagerState(t *testing.T) {
	fakeServiceManager(t, &fakeLifecycle{status: service.StatusStopped})

	out, err := run(t, "", "daemon", "status")
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	if !strings.Contains(out, "installed, stopped") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSurfacesAnUnavailableServiceManager(t *testing.T) {
	boom := errors.New("no service manager here")

	for _, args := range [][]string{
		{"daemon", "start"},
		{"daemon", "stop"},
		{"daemon", "status"},
		{"uninstall", "--daemon-only"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			unavailableServiceManager(t, boom)
			if _, err := run(t, "", args...); !errors.Is(err, boom) {
				t.Errorf("%v: err = %v, want the service-manager failure", args, err)
			}
		})
	}

	t.Run("install", func(t *testing.T) {
		unavailableServiceManager(t, boom)
		var out bytes.Buffer
		err := runInstall(&out, installOptions{binDir: installedCLI(t), daemonOnly: true})
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the service-manager failure", err)
		}
	})
}

func TestCmdInstallDaemonOnly(t *testing.T) {
	f := &fakeLifecycle{}
	fakeServiceManager(t, f)

	out, err := run(t, "", "install", "--daemon-only", "--bin-dir", installedCLI(t))
	if err != nil {
		t.Fatalf("install --daemon-only: %v", err)
	}
	if !f.installed {
		t.Error("install --daemon-only did not register the daemon")
	}
	if !strings.Contains(out, "daemon installed") {
		t.Errorf("out = %q", out)
	}
}

func TestRunInstallRegistersTheDaemonAgainstTheInstalledCLI(t *testing.T) {
	f := &fakeLifecycle{}
	var registered string
	prev := newLifecycle
	newLifecycle = func(_, executable string) (lifecycle, error) {
		registered = executable
		return f, nil
	}
	t.Cleanup(func() { newLifecycle = prev })

	srcDir := t.TempDir()
	exe := filepath.Join(srcDir, "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)

	var out bytes.Buffer
	if err := runInstall(&out, installOptions{exe: exe, binDir: binDir}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	want := filepath.Join(binDir, cliBinaryName)
	if registered != want {
		t.Errorf("daemon registered against %q, want the installed CLI copy %q", registered, want)
	}
	if !f.installed {
		t.Error("the daemon was not registered")
	}
	if strings.Contains(out.String(), "warning") {
		t.Errorf("out = %q, want no PATH warning when the bin dir is on PATH", out.String())
	}
}

func TestRunInstallDaemonOnlyUsesTheInstalledCLIWhenPresent(t *testing.T) {
	binDir := installedCLI(t)
	var registered string
	prev := newLifecycle
	newLifecycle = func(_, executable string) (lifecycle, error) {
		registered = executable
		return &fakeLifecycle{}, nil
	}
	t.Cleanup(func() { newLifecycle = prev })

	var out bytes.Buffer
	if err := runInstall(&out, installOptions{
		exe: "/tmp/go-build123/b001/exe/lumberjack", binDir: binDir, daemonOnly: true,
	}); err != nil {
		t.Fatalf("runInstall --daemon-only: %v", err)
	}
	if registered != filepath.Join(binDir, cliBinaryName) {
		t.Errorf("registered %q, want the installed CLI copy", registered)
	}
}

func TestRunInstallDaemonOnlyRefusesAnEphemeralBinary(t *testing.T) {
	var out bytes.Buffer
	err := runInstall(&out, installOptions{
		exe: "/tmp/go-build123/b001/exe/lumberjack", binDir: t.TempDir(), daemonOnly: true,
	})
	if err == nil || !strings.Contains(err.Error(), "go run") {
		t.Errorf("err = %v, want the go-run guard with no installed CLI to fall back to", err)
	}
}

func TestRunInstallSurfacesADaemonRegistrationFailure(t *testing.T) {
	fakeServiceManager(t, &fakeLifecycle{installErr: errors.New("boom")})

	var out bytes.Buffer
	err := runInstall(&out, installOptions{binDir: installedCLI(t), daemonOnly: true})
	if err == nil || !strings.Contains(err.Error(), "installing daemon") {
		t.Errorf("err = %v, want the registration failure", err)
	}
}

func TestRunInstallWithoutAResolvableHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")

	var out bytes.Buffer
	if err := runInstall(&out, installOptions{cliOnly: true}); err == nil {
		t.Error("expected runInstall to fail with no home directory to default the bin dir to")
	}
	if err := runUninstall(&out, uninstallOptions{daemonOnly: false, cliOnly: true}); err == nil {
		t.Error("expected runUninstall to fail with no home directory to default the bin dir to")
	}
}

func TestRunInstallSurfacesAFailedPathWarningWrite(t *testing.T) {
	srcDir := t.TempDir()
	exe := filepath.Join(srcDir, "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin")

	err := runInstall(failWriter{}, installOptions{exe: exe, binDir: t.TempDir(), cliOnly: true})
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}

func TestCmdUninstall(t *testing.T) {
	f := &fakeLifecycle{}
	fakeServiceManager(t, f)
	binDir := installedCLI(t)

	out, err := run(t, "", "uninstall", "--bin-dir", binDir)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !f.stopped || !f.uninstalled {
		t.Errorf("uninstall did not deregister the daemon (stopped=%v uninstalled=%v)", f.stopped, f.uninstalled)
	}
	if _, err := os.Stat(filepath.Join(binDir, cliBinaryName)); !os.IsNotExist(err) {
		t.Errorf("expected the installed CLI to be removed, got %v", err)
	}
	if !strings.Contains(out, "daemon uninstalled") || !strings.Contains(out, "CLI removed") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdUninstallCLIOnlyLeavesTheDaemonAlone(t *testing.T) {
	f := &fakeLifecycle{}
	fakeServiceManager(t, f)

	if _, err := run(t, "", "uninstall", "--cli-only", "--bin-dir", installedCLI(t)); err != nil {
		t.Fatalf("uninstall --cli-only: %v", err)
	}
	if f.uninstalled {
		t.Error("--cli-only deregistered the daemon")
	}
}

func TestRunUninstallSurfacesADaemonFailure(t *testing.T) {
	fakeServiceManager(t, &fakeLifecycle{uninstallErr: errors.New("boom")})

	var out bytes.Buffer
	err := runUninstall(&out, uninstallOptions{binDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "uninstalling daemon") {
		t.Errorf("err = %v, want the deregistration failure", err)
	}
}

func TestRunUninstallSurfacesAFailedRemoval(t *testing.T) {
	fakeServiceManager(t, &fakeLifecycle{})
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runUninstall(&out, uninstallOptions{binDir: notADir, daemonOnly: false})
	if err == nil || !strings.Contains(err.Error(), "removing") {
		t.Errorf("err = %v, want the failed removal", err)
	}
}

func TestUninstallDaemonSurfacesAFailedWrite(t *testing.T) {
	if err := uninstallDaemon(failWriter{}, &fakeLifecycle{}); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}

func TestInstallDaemonSurfacesAFailedWrite(t *testing.T) {
	if err := installDaemon(failWriter{}, &fakeLifecycle{}, false); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}

func TestUninstallCLISurfacesFailedWrites(t *testing.T) {
	binDir := installedCLI(t)
	if err := uninstallCLI(failWriter{}, binDir); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
	if err := uninstallCLI(failWriter{}, t.TempDir()); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed not-found write", err)
	}
}

func TestInstallCLISurfacesAFailedWrite(t *testing.T) {
	srcDir := t.TempDir()
	exe := filepath.Join(srcDir, "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(failWriter{}, exe, t.TempDir(), false); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}

func TestInstallCLIRejectsAnUnusableDestination(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(io.Discard, notADir, notADir, false); err == nil {
		t.Error("expected installCLI to fail when the destination's parent is a file")
	}

	readOnly := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(io.Discard, notADir, filepath.Join(readOnly, "bin"), false); err == nil {
		t.Error("expected installCLI to fail when the destination cannot be created")
	}
}

func TestCopyExecutableFailures(t *testing.T) {
	dir := t.TempDir()

	if err := copyExecutable(filepath.Join(dir, "absent"), filepath.Join(dir, "dest")); err == nil {
		t.Error("expected a missing source to fail")
	}

	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(src, filepath.Join(dir, "absent-dir", "dest")); err == nil {
		t.Error("expected a missing destination directory to fail")
	}

	if err := copyExecutable(dir, filepath.Join(dir, "dest")); err == nil {
		t.Error("expected copying a directory to fail")
	}
}

func TestDefaultBinDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := defaultBinDir()
	if err != nil {
		t.Fatalf("defaultBinDir: %v", err)
	}
	if want := filepath.Join(home, ".local", "bin"); got != want {
		t.Errorf("defaultBinDir = %q, want %q", got, want)
	}

	t.Setenv("HOME", "")
	if _, err := defaultBinDir(); err == nil {
		t.Error("expected defaultBinDir to fail with no home directory")
	}
}

func TestReportStatusSurfacesAFailedWrite(t *testing.T) {
	err := reportStatus(failWriter{}, &fakeLifecycle{statusErr: service.ErrNotInstalled}, present.Structured)
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}

func TestReportStatusSurfacesAManagerFailure(t *testing.T) {
	err := reportStatus(io.Discard, &fakeLifecycle{statusErr: errors.New("boom")}, present.Structured)
	if err == nil || !strings.Contains(err.Error(), "checking daemon status") {
		t.Errorf("err = %v, want the wrapped status failure", err)
	}
}

func TestStartStopDaemonSurfaceManagerFailures(t *testing.T) {
	boom := errors.New("boom")

	if err := startDaemon(io.Discard, &fakeLifecycle{statusErr: boom}, present.Structured); err == nil ||
		!strings.Contains(err.Error(), "checking daemon status") {
		t.Errorf("startDaemon err = %v, want the wrapped status failure", err)
	}
	if err := startDaemon(io.Discard, &fakeLifecycle{startErr: boom}, present.Structured); err == nil ||
		!strings.Contains(err.Error(), "starting daemon") {
		t.Errorf("startDaemon err = %v, want the wrapped start failure", err)
	}
	if err := stopDaemon(io.Discard, &fakeLifecycle{statusErr: boom}, present.Structured); err == nil ||
		!strings.Contains(err.Error(), "checking daemon status") {
		t.Errorf("stopDaemon err = %v, want the wrapped status failure", err)
	}
	if err := stopDaemon(io.Discard, &fakeLifecycle{status: service.StatusRunning, stopErr: boom}, present.Structured); err == nil ||
		!strings.Contains(err.Error(), "stopping daemon") {
		t.Errorf("stopDaemon err = %v, want the wrapped stop failure", err)
	}
}

func TestEmitDaemonMessageJSON(t *testing.T) {
	var out bytes.Buffer
	if err := emitDaemonMessage(&out, present.JSON, "hello"); err != nil {
		t.Fatalf("emitDaemonMessage: %v", err)
	}
	if !strings.Contains(out.String(), `"message"`) || !strings.Contains(out.String(), "hello") {
		t.Errorf("out = %q, want a JSON view model", out.String())
	}
}

func TestServiceEnvCarriesPath(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin")
	if got := serviceEnv()["PATH"]; got != "/opt/homebrew/bin" {
		t.Errorf("serviceEnv PATH = %q, want the install-time PATH", got)
	}

	t.Setenv("PATH", "")
	if _, ok := serviceEnv()["PATH"]; ok {
		t.Error("serviceEnv should omit an empty PATH rather than pinning it")
	}
}

func TestProgramStartSurfacesAWiringFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUMBERJACK_DB_PATH", filepath.Join(blocked, "db.sqlite"))

	p := &program{socketPath: filepath.Join(t.TempDir(), "d.sock")}
	if err := p.Start(nil); err == nil {
		t.Error("expected Start to fail when the database cannot be opened")
		_ = p.Stop(nil)
	}
}

func TestProgramStopWithoutAStartedApp(t *testing.T) {
	p := &program{}
	if err := p.Stop(nil); err != nil {
		t.Errorf("Stop before Start = %v, want nil", err)
	}

	p.app = fx.New(fx.NopLogger)
	if err := p.Stop(nil); err != nil {
		t.Errorf("Stop = %v, want nil", err)
	}
}
