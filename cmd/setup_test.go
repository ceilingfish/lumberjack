package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRepo makes t's temp dir look like a git worktree root (a `.git` entry is
// all setup.RepoRoot needs) and chdirs into it, so the daemon-free `setup`
// commands resolve their config there.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir .git: %v", err)
	}
	t.Chdir(dir)
	return dir
}

func TestCmdSetupListEmpty(t *testing.T) {
	setupRepo(t)
	out, err := run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if !strings.Contains(out, "No setup commands") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSetupAddListRemove(t *testing.T) {
	dir := setupRepo(t)

	if _, err := run(t, "", "setup-steps", "add", "go mod download"); err != nil {
		t.Fatalf("setup-steps add: %v", err)
	}
	if _, err := run(t, "", "setup-steps", "add", "npm install"); err != nil {
		t.Fatalf("setup-steps add: %v", err)
	}

	// list preserves insertion order.
	out, err := run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if want := "go mod download\nnpm install\n"; !strings.Contains(out, want) {
		t.Errorf("out = %q, want it to contain %q in order", out, want)
	}

	// The config was actually written.
	if _, err := os.Stat(filepath.Join(dir, ".lumberjack.yml")); err != nil {
		t.Fatalf("expected .lumberjack.yml to be written: %v", err)
	}

	// remove drops just the named command.
	if _, err := run(t, "", "setup-steps", "remove", "go mod download"); err != nil {
		t.Fatalf("setup-steps remove: %v", err)
	}
	out, err = run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if strings.Contains(out, "go mod download") || !strings.Contains(out, "npm install") {
		t.Errorf("after remove, out = %q", out)
	}
}

func TestCmdSetupAddIsIdempotent(t *testing.T) {
	setupRepo(t)
	if _, err := run(t, "", "setup-steps", "add", "make build"); err != nil {
		t.Fatalf("setup-steps add: %v", err)
	}
	out, err := run(t, "", "setup-steps", "add", "make build")
	if err != nil {
		t.Fatalf("setup-steps add (dup): %v", err)
	}
	if !strings.Contains(out, "already present") {
		t.Errorf("out = %q, want an already-present note", out)
	}
	// Only one entry recorded.
	out, err = run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if strings.Count(out, "make build") != 1 {
		t.Errorf("out = %q, want exactly one entry", out)
	}
}

func TestCmdSetupRemoveMissingErrors(t *testing.T) {
	setupRepo(t)
	_, err := run(t, "", "setup-steps", "remove", "nonexistent")
	if err == nil || !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected an error naming the missing command, got %v", err)
	}
}

func TestCmdSetupListJSON(t *testing.T) {
	setupRepo(t)
	if _, err := run(t, "", "setup-steps", "add", "go test ./..."); err != nil {
		t.Fatalf("setup-steps add: %v", err)
	}
	out, err := run(t, "", "setup-steps", "list", "--format", "json")
	if err != nil {
		t.Fatalf("setup-steps list --format json: %v", err)
	}
	if !strings.Contains(out, `["go test ./..."]`) {
		t.Errorf("out = %q, want a JSON array", out)
	}
}
