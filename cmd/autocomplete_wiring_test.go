package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/autocomplete"
	"github.com/ceilingfish/lumberjack/internal/cli"
)

func fakeInteractive(t *testing.T, v bool) {
	t.Helper()
	prev := cli.InteractiveTerminal
	cli.InteractiveTerminal = func() bool { return v }
	t.Cleanup(func() { cli.InteractiveTerminal = prev })
}

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PROFILE", filepath.Join(home, "profile.ps1"))
	return home
}

func scriptConfirm(t *testing.T, answers ...bool) *[]string {
	t.Helper()
	var asked []string
	prev := autocomplete.Confirmer
	autocomplete.Confirmer = func(_ io.Writer, rc, _ string) (bool, error) {
		asked = append(asked, rc)
		if len(answers) == 0 {
			return false, nil
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	t.Cleanup(func() { autocomplete.Confirmer = prev })
	return &asked
}

func TestRunInstallCompletionFailureLeavesExitCodeZero(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	if err := os.Mkdir(filepath.Join(home, ".zshrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	err := runInstall(&out, installOptions{
		exe: exe, binDir: t.TempDir(), cliOnly: true,
		autocompleteShell: "zsh", errOut: &errOut,
	})
	if err != nil {
		t.Fatalf("runInstall = %v, want nil despite the completion failure", err)
	}
	if !strings.Contains(errOut.String(), ".zshrc") {
		t.Errorf("errOut = %q, want a warning", errOut.String())
	}
}

func TestRunInstallRejectsConflictingAutocompleteFlags(t *testing.T) {
	var out bytes.Buffer
	err := runInstall(&out, installOptions{
		cliOnly: true, autocompleteShell: "zsh", noAutocomplete: true,
	})
	if !errors.Is(err, autocomplete.ErrExclusive) {
		t.Errorf("runInstall err = %v, want autocomplete.ErrExclusive", err)
	}
}

func TestRunUninstallScansRCFilesForCompletion(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("export A=1\n"+autocomplete.Line("zsh")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := scriptConfirm(t, true)

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, cli.BinaryName), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := runUninstall(&out, uninstallOptions{binDir: binDir, cliOnly: true, errOut: &errOut}); err != nil {
		t.Fatalf("runUninstall: %v", err)
	}

	if len(*asked) != 1 || (*asked)[0] != rc {
		t.Fatalf("prompted for %v, want [%s]", *asked, rc)
	}
	body, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), autocomplete.Line("zsh")) {
		t.Errorf(".zshrc still sources completion: %q", body)
	}
	if !strings.Contains(string(body), "export A=1") {
		t.Errorf("unrelated rc lines were dropped: %q", body)
	}
	if !strings.Contains(out.String(), rc) {
		t.Errorf("out = %q, want the edited rc reported", out.String())
	}
}

func TestRunUninstallDaemonOnlyLeavesRCFilesAlone(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	rc := filepath.Join(home, ".zshrc")
	body := autocomplete.Line("zsh") + "\n"
	if err := os.WriteFile(rc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := scriptConfirm(t, true)
	fakeServiceManager(t, &fakeLifecycle{})

	var out, errOut bytes.Buffer
	if err := runUninstall(&out, uninstallOptions{daemonOnly: true, errOut: &errOut}); err != nil {
		t.Fatalf("runUninstall --daemon-only: %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted under --daemon-only: %v", *asked)
	}
	got, _ := os.ReadFile(rc)
	if string(got) != body {
		t.Errorf(".zshrc = %q, want it untouched", got)
	}
}
