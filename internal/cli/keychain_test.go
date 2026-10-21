package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/present"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

type keychainFixture struct {
	config string
	added  []string
	addErr error
	stderr bytes.Buffer
}

func newKeychainFixture(t *testing.T, interactive bool) *keychainFixture {
	t.Helper()
	f := &keychainFixture{config: filepath.Join(t.TempDir(), ".ssh", "config")}
	prevAdd, prevPath, prevTerm := SSHAdd, SSHConfigPath, InteractiveTerminal
	SSHAdd = func(_ *cobra.Command, path string) error {
		f.added = append(f.added, path)
		return f.addErr
	}
	SSHConfigPath = func() (string, error) { return f.config, nil }
	InteractiveTerminal = func() bool { return interactive }
	t.Cleanup(func() { SSHAdd, SSHConfigPath, InteractiveTerminal = prevAdd, prevPath, prevTerm })
	return f
}

func (f *keychainFixture) cmd(in string) *cobra.Command {
	c := cmdWith(in, &bytes.Buffer{})
	c.SetErr(&f.stderr)
	return c
}

func TestPromptSSHKeychainIsSilentWithNothingToReport(t *testing.T) {
	f := newKeychainFixture(t, true)
	if err := PromptSSHKeychain(f.cmd("y\n"), true, nil, &lumberjackv1.SshKeychainCheck{}); err != nil {
		t.Fatalf("PromptSSHKeychain: %v", err)
	}
	if f.stderr.Len() != 0 || len(f.added) != 0 {
		t.Errorf("stderr = %q, added = %v", f.stderr.String(), f.added)
	}
}

func TestPromptSSHKeychainRunsTheFixWhenAccepted(t *testing.T) {
	f := newKeychainFixture(t, true)
	checks := []*lumberjackv1.SshKeychainCheck{
		{LockedKeys: []string{"/k1", "/k2"}},
		{LockedKeys: []string{"/k1"}, UseKeychainDisabled: true},
	}
	if err := PromptSSHKeychain(f.cmd("y\n"), true, checks...); err != nil {
		t.Fatalf("PromptSSHKeychain: %v", err)
	}
	if !slices.Equal(f.added, []string{"/k1", "/k2"}) {
		t.Errorf("added = %v, want [/k1 /k2]", f.added)
	}
	data, err := os.ReadFile(f.config)
	if err != nil || !strings.Contains(string(data), "UseKeychain yes") {
		t.Errorf("config = %q, %v", data, err)
	}
	for _, want := range []string{"/k1", "ssh-add --apple-use-keychain /k2", "UseKeychain", "Run these now?", "Added UseKeychain"} {
		if !strings.Contains(f.stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", f.stderr.String(), want)
		}
	}
}

func TestPromptSSHKeychainOnlyAdvisesWhenDeclined(t *testing.T) {
	f := newKeychainFixture(t, true)
	check := &lumberjackv1.SshKeychainCheck{LockedKeys: []string{"/k"}, UseKeychainDisabled: true}
	if err := PromptSSHKeychain(f.cmd("n\n"), true, check); err != nil {
		t.Fatalf("PromptSSHKeychain: %v", err)
	}
	if len(f.added) != 0 {
		t.Errorf("added = %v, want none", f.added)
	}
	if _, err := os.Stat(f.config); !os.IsNotExist(err) {
		t.Errorf("config written despite declining: %v", err)
	}
}

func TestPromptSSHKeychainNeverPromptsOffATerminal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		terminal    bool
		interactive bool
	}{
		{"no terminal", false, true},
		{"non-interactive caller", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newKeychainFixture(t, tc.terminal)
			check := &lumberjackv1.SshKeychainCheck{LockedKeys: []string{"/k"}}
			if err := PromptSSHKeychain(f.cmd("y\n"), tc.interactive, check); err != nil {
				t.Fatalf("PromptSSHKeychain: %v", err)
			}
			if len(f.added) != 0 || strings.Contains(f.stderr.String(), "Run these now?") {
				t.Errorf("prompted: stderr = %q, added = %v", f.stderr.String(), f.added)
			}
			if !strings.Contains(f.stderr.String(), "ssh-add --apple-use-keychain /k") {
				t.Errorf("stderr = %q, want the fix command", f.stderr.String())
			}
		})
	}
}

func TestPromptSSHKeychainWarnsWhenTheFixFails(t *testing.T) {
	f := newKeychainFixture(t, true)
	f.addErr = errors.New("bad passphrase")
	f.config = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f.config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.config = filepath.Join(f.config, "config")
	check := &lumberjackv1.SshKeychainCheck{LockedKeys: []string{"/k"}, UseKeychainDisabled: true}
	if err := PromptSSHKeychain(f.cmd("y\n"), true, check); err != nil {
		t.Fatalf("PromptSSHKeychain: %v", err)
	}
	for _, want := range []string{"ssh-add /k failed: bad passphrase", "warning: updating"} {
		if !strings.Contains(f.stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", f.stderr.String(), want)
		}
	}
}

func TestPromptSSHKeychainSurfacesAConfigPathError(t *testing.T) {
	newKeychainFixture(t, true)
	SSHConfigPath = func() (string, error) { return "", errors.New("no home") }
	check := &lumberjackv1.SshKeychainCheck{UseKeychainDisabled: true}
	if err := PromptSSHKeychain(cmdWith("", &bytes.Buffer{}), true, check); err == nil {
		t.Error("PromptSSHKeychain err = nil, want the config path error")
	}
}

func TestRunSyncAdvisesOnLockedSSHKeys(t *testing.T) {
	f := newKeychainFixture(t, false)
	events := syncEvents()
	events[len(events)-1].Summary.SshKeychain = &lumberjackv1.SshKeychainCheck{LockedKeys: []string{"/k"}}
	serve(t, &stub{syncEvents: events})

	if err := RunSync(context.Background(), f.cmd(""), dial(t), "n", present.Structured); err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if !strings.Contains(f.stderr.String(), "ssh-add --apple-use-keychain /k") {
		t.Errorf("stderr = %q, want the fix command", f.stderr.String())
	}
}

func TestDescribeSSHKeychainSurfacesFailedWrites(t *testing.T) {
	for succeed := range 6 {
		w := &flakyWriter{succeed: succeed}
		if err := describeSSHKeychain(w, []string{"/k"}, true, "/config"); !errors.Is(err, errWrite) {
			t.Errorf("succeed=%d: err = %v, want the failed write", succeed, err)
		}
	}
}

func TestPromptSSHKeychainSurfacesAFailedDescription(t *testing.T) {
	f := newKeychainFixture(t, true)
	c := cmdWith("", &bytes.Buffer{})
	c.SetErr(failWriter{})
	check := &lumberjackv1.SshKeychainCheck{LockedKeys: []string{"/k"}}
	if err := PromptSSHKeychain(c, true, check); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
	if len(f.added) != 0 {
		t.Errorf("added = %v, want none", f.added)
	}
}

func TestAppendUseKeychainSurfacesAnUnwritableConfig(t *testing.T) {
	if err := appendUseKeychain(t.TempDir()); err == nil {
		t.Error("appendUseKeychain err = nil, want the open failure")
	}
}

func TestSSHConfigPathDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := SSHConfigPath()
	if err != nil || got != filepath.Join(home, ".ssh", "config") {
		t.Errorf("SSHConfigPath = %q, %v", got, err)
	}
	t.Setenv("HOME", "")
	if _, err := SSHConfigPath(); err == nil {
		t.Error("SSHConfigPath err = nil, want the missing home error")
	}
}

func TestSSHAddRunsTheKeychainVariant(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\necho added >&2\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh-add"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var stderr bytes.Buffer
	c := cmdWith("", &bytes.Buffer{})
	c.SetErr(&stderr)
	c.SetContext(context.Background())
	if err := SSHAdd(c, "/k"); err != nil {
		t.Fatalf("SSHAdd: %v", err)
	}
	args, _ := os.ReadFile(log)
	if strings.TrimSpace(string(args)) != "--apple-use-keychain /k" || strings.TrimSpace(stderr.String()) != "added" {
		t.Errorf("args = %q, stderr = %q", args, stderr.String())
	}
}
