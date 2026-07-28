package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeWorktree builds a main checkout (a `.git` directory) plus a linked
// worktree pointing at it the way `git worktree add` does (a `.git` file
// holding a gitdir pointer), which is all the locating code reads.
func fakeWorktree(t *testing.T) (main, worktree string) {
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
	return main, worktree
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(configPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", ConfigFileName, err)
	}
}

func TestMainCheckoutOfMainCheckout(t *testing.T) {
	main, _ := fakeWorktree(t)
	got, err := MainCheckout(main)
	if err != nil {
		t.Fatalf("MainCheckout: %v", err)
	}
	if got != main {
		t.Fatalf("MainCheckout(%q) = %q, want itself", main, got)
	}
}

func TestMainCheckoutOfLinkedWorktree(t *testing.T) {
	main, worktree := fakeWorktree(t)
	got, err := MainCheckout(worktree)
	if err != nil {
		t.Fatalf("MainCheckout: %v", err)
	}
	if got != main {
		t.Fatalf("MainCheckout(%q) = %q, want %q", worktree, got, main)
	}
}

func TestResolveInheritsMainCheckoutConfig(t *testing.T) {
	main, worktree := fakeWorktree(t)
	writeConfig(t, main, "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n")

	res, err := Resolve(worktree)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Inherited {
		t.Error("want the config reported as inherited")
	}
	if res.ConfigPath != configPath(main) {
		t.Errorf("ConfigPath = %q, want %q", res.ConfigPath, configPath(main))
	}
	if res.Worktree != worktree || res.MainCheckout != main {
		t.Errorf("Worktree/MainCheckout = %q/%q, want %q/%q", res.Worktree, res.MainCheckout, worktree, main)
	}
	if cmds := res.Config.RunCommands(); len(cmds) != 1 || cmds[0] != "make setup" {
		t.Errorf("RunCommands() = %v, want the inherited command", cmds)
	}
}

func TestResolveLocalConfigOverridesMainCheckout(t *testing.T) {
	main, worktree := fakeWorktree(t)
	writeConfig(t, main, "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n")
	writeConfig(t, worktree, "steps:\n  - type: run-command\n    run_command:\n      command: make local\n")

	res, err := Resolve(worktree)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Inherited {
		t.Error("want the local config used, not inherited")
	}
	// Override, not merge: only the local file's steps run.
	if cmds := res.Config.RunCommands(); len(cmds) != 1 || cmds[0] != "make local" {
		t.Errorf("RunCommands() = %v, want only the local command", cmds)
	}
}

func TestResolveNoConfigAnywhere(t *testing.T) {
	_, worktree := fakeWorktree(t)
	res, err := Resolve(worktree)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.ConfigPath != "" || len(res.Config.Steps) != 0 {
		t.Fatalf("ConfigPath = %q with %d steps, want an empty config", res.ConfigPath, len(res.Config.Steps))
	}
}

func TestResolveFromSubdirectoryOfWorktree(t *testing.T) {
	main, worktree := fakeWorktree(t)
	writeConfig(t, main, "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n")
	sub := filepath.Join(worktree, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	res, err := Resolve(sub)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Worktree != worktree || !res.Inherited {
		t.Fatalf("Resolve(%q) = worktree %q inherited=%v", sub, res.Worktree, res.Inherited)
	}
}
