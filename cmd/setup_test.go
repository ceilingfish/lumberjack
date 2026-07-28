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

// setupLinkedWorktree makes a main checkout holding config and a linked
// worktree pointing at it (a `.git` file with a gitdir pointer, as
// `git worktree add` writes), chdirs into the worktree, and returns both paths.
func setupLinkedWorktree(t *testing.T, config string) (main, worktree string) {
	t.Helper()
	root := t.TempDir()
	main, worktree = filepath.Join(root, "main"), filepath.Join(root, "wt")
	if err := os.MkdirAll(filepath.Join(main, ".git", "worktrees", "wt"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	pointer := "gitdir: " + filepath.Join(main, ".git", "worktrees", "wt") + "\n"
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(pointer), 0o644); err != nil {
		t.Fatalf("WriteFile .git: %v", err)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(main, ".lumberjack.yml"), []byte(config), 0o644); err != nil {
			t.Fatalf("WriteFile .lumberjack.yml: %v", err)
		}
	}
	t.Chdir(worktree)
	return main, worktree
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

func TestCmdSetupListInheritsFromMainCheckout(t *testing.T) {
	setupLinkedWorktree(t, "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n")
	out, err := run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if !strings.Contains(out, "make setup") {
		t.Errorf("out = %q, want the main checkout's command inherited", out)
	}
}

func TestCmdSetupListLocalConfigOverrides(t *testing.T) {
	_, worktree := setupLinkedWorktree(t, "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n")
	local := "steps:\n  - type: run-command\n    run_command:\n      command: make local\n"
	if err := os.WriteFile(filepath.Join(worktree, ".lumberjack.yml"), []byte(local), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out, err := run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if strings.Contains(out, "make setup") || !strings.Contains(out, "make local") {
		t.Errorf("out = %q, want only the local command", out)
	}
}

func TestCmdSetupAddInWorktreeKeepsInheritedSteps(t *testing.T) {
	_, worktree := setupLinkedWorktree(t, "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n")
	if _, err := run(t, "", "setup-steps", "add", "npm install"); err != nil {
		t.Fatalf("setup-steps add: %v", err)
	}
	// The override written here carries the inherited step rather than losing it.
	if _, err := os.Stat(filepath.Join(worktree, ".lumberjack.yml")); err != nil {
		t.Fatalf("expected an override written in the worktree: %v", err)
	}
	out, err := run(t, "", "setup-steps", "list")
	if err != nil {
		t.Fatalf("setup-steps list: %v", err)
	}
	if want := "make setup\nnpm install\n"; !strings.Contains(out, want) {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestCmdSetupRunInheritedSteps(t *testing.T) {
	main, worktree := setupLinkedWorktree(t, "steps:\n"+
		"  - type: copy-file\n    copy_file:\n      source: .env\n      destination: .env\n"+
		"  - type: run-command\n    run_command:\n      command: touch ran.txt\n")
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile .env: %v", err)
	}

	out, err := run(t, "", "setup-steps", "run")
	if err != nil {
		t.Fatalf("setup-steps run: %v", err)
	}
	if !strings.Contains(out, "Inheriting setup steps") {
		t.Errorf("out = %q, want a note that the config was inherited", out)
	}
	// copy-file resolves its source against the main checkout, its destination
	// against this worktree; run-command runs here without a consent prompt.
	if _, err := os.Stat(filepath.Join(worktree, ".env")); err != nil {
		t.Errorf("expected .env copied into the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "ran.txt")); err != nil {
		t.Errorf("expected the run-command to have executed: %v", err)
	}
}

func TestCmdSetupRunNoSteps(t *testing.T) {
	setupRepo(t)
	out, err := run(t, "", "setup-steps", "run")
	if err != nil {
		t.Fatalf("setup-steps run: %v", err)
	}
	if !strings.Contains(out, "No setup steps configured") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSetupRunFailingStepErrors(t *testing.T) {
	setupLinkedWorktree(t, "steps:\n  - type: run-command\n    run_command:\n      command: exit 3\n")
	_, err := run(t, "", "setup-steps", "run")
	if err == nil || !strings.Contains(err.Error(), "run-command") {
		t.Errorf("expected an error naming the failed step, got %v", err)
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
