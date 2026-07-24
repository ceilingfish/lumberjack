package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kardianos/service"
)

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
		"/var/folders/xb/T/go-build2170204171/b001/exe/lumberjack",    // temp work dir
		"/tmp/go-build123/b001/exe/lumberjack",                        // temp work dir
		"/Users/tom/Library/Caches/go-build/42/42dd568…-d/lumberjack", // persistent build cache
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

// TestResolveDaemonExecutable: an already-installed CLI copy is preferred
// (whatever binary is currently running); absent that, the running executable
// is used subject to the go-run guard.
func TestResolveDaemonExecutable(t *testing.T) {
	got, err := resolveDaemonExecutable("/anything", "/home/u/.local/bin/lumberjack", true)
	if err != nil {
		t.Fatalf("resolveDaemonExecutable (installed): %v", err)
	}
	if got != "/home/u/.local/bin/lumberjack" {
		t.Errorf("got %q, want the installed CLI path", got)
	}

	got, err = resolveDaemonExecutable("/usr/local/bin/lumberjack", "/home/u/.local/bin/lumberjack", false)
	if err != nil {
		t.Fatalf("resolveDaemonExecutable (durable exe): %v", err)
	}
	if got != "/usr/local/bin/lumberjack" {
		t.Errorf("got %q, want the running executable", got)
	}

	_, err = resolveDaemonExecutable("/tmp/go-build123/b001/exe/lumberjack", "/home/u/.local/bin/lumberjack", false)
	if err == nil {
		t.Error("expected the go-run guard to reject an ephemeral running executable")
	}
}

func TestBinDirOnPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+"/home/u/.local/bin")
	if !binDirOnPath("/home/u/.local/bin") {
		t.Error("expected /home/u/.local/bin to be found on PATH")
	}
	if binDirOnPath("/opt/nowhere") {
		t.Error("expected /opt/nowhere to not be found on PATH")
	}
}

// TestInstallCLI covers the copy-to-destination behaviour directly: a fresh
// install, a refused overwrite without --force, and an upgrade with --force.
func TestInstallCLI(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "src-binary")
	if err := os.WriteFile(src, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	var buf bytes.Buffer
	dest, err := installCLI(&buf, src, destDir, false)
	if err != nil {
		t.Fatalf("installCLI: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1" {
		t.Errorf("installed content = %q, want %q", data, "v1")
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("installed binary is not executable: mode %v", info.Mode())
	}

	// Reinstalling without --force is refused.
	if _, err := installCLI(&buf, src, destDir, false); err == nil {
		t.Error("expected an error reinstalling without --force")
	}

	// --force overwrites.
	if err := os.WriteFile(src, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(&buf, src, destDir, true); err != nil {
		t.Fatalf("installCLI --force: %v", err)
	}
	data, err = os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2" {
		t.Errorf("installed content after --force = %q, want %q", data, "v2")
	}
}

// TestRunInstallMutualExclusivity: --daemon-only and --cli-only together are
// rejected before touching the filesystem or service manager.
func TestRunInstallMutualExclusivity(t *testing.T) {
	var buf bytes.Buffer
	err := runInstall(&buf, installOptions{daemonOnly: true, cliOnly: true})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v, want a mutual-exclusivity message", err)
	}
}

// TestRunInstallCLIOnly drives runInstall end-to-end for --cli-only, which
// never touches the real service manager: the running "executable" is a
// fixture file, and the bin dir is a temp directory.
func TestRunInstallCLIOnly(t *testing.T) {
	srcDir := t.TempDir()
	exe := filepath.Join(srcDir, "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	t.Setenv("PATH", "/usr/bin") // binDir deliberately absent, to exercise the warning

	var buf bytes.Buffer
	err := runInstall(&buf, installOptions{exe: exe, binDir: binDir, cliOnly: true})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(binDir, cliBinaryName)); err != nil {
		t.Errorf("CLI was not installed: %v", err)
	}
	if !strings.Contains(buf.String(), "warning") || !strings.Contains(buf.String(), "PATH") {
		t.Errorf("expected a PATH warning, out = %q", buf.String())
	}
}

// TestRunInstallCLIOnlyRefusesEphemeral: the go-run guard applies to
// --cli-only installs too, not just the daemon registration.
func TestRunInstallCLIOnlyRefusesEphemeral(t *testing.T) {
	var buf bytes.Buffer
	err := runInstall(&buf, installOptions{
		exe:     "/tmp/go-build123/b001/exe/lumberjack",
		binDir:  t.TempDir(),
		cliOnly: true,
	})
	if err == nil {
		t.Fatal("expected the go-run guard to reject an ephemeral executable")
	}
}

func TestRunUninstallMutualExclusivity(t *testing.T) {
	var buf bytes.Buffer
	err := runUninstall(&buf, uninstallOptions{daemonOnly: true, cliOnly: true})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v, want a mutual-exclusivity message", err)
	}
}

// TestUninstallCLI covers removing an installed binary and reports a missing
// one without erroring, since uninstall's goal state is already met.
func TestUninstallCLI(t *testing.T) {
	binDir := t.TempDir()
	dest := filepath.Join(binDir, cliBinaryName)
	if err := os.WriteFile(dest, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := uninstallCLI(&buf, binDir); err != nil {
		t.Fatalf("uninstallCLI: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed", dest)
	}

	// A second uninstall (nothing there) is not an error.
	buf.Reset()
	if err := uninstallCLI(&buf, binDir); err != nil {
		t.Fatalf("uninstallCLI on missing binary: %v", err)
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("out = %q, want a not-found message", buf.String())
	}
}

func TestUninstallDaemon(t *testing.T) {
	var buf bytes.Buffer
	f := &fakeLifecycle{}
	if err := uninstallDaemon(&buf, f); err != nil {
		t.Fatalf("uninstallDaemon: %v", err)
	}
	if !f.stopped || !f.uninstalled {
		t.Error("expected Stop and Uninstall to be called")
	}

	// Not-installed is not an error.
	f = &fakeLifecycle{uninstallErr: service.ErrNotInstalled}
	if err := uninstallDaemon(&buf, f); err != nil {
		t.Fatalf("uninstallDaemon over not-installed: %v", err)
	}

	// A real failure surfaces.
	f = &fakeLifecycle{uninstallErr: errors.New("boom")}
	if err := uninstallDaemon(&buf, f); err == nil {
		t.Error("expected uninstall error to surface")
	}
}
