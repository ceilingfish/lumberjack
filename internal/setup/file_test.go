package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddCommand(t *testing.T) {
	cfg := &Config{}
	if !cfg.AddCommand("go mod download") {
		t.Fatal("AddCommand: want true adding a new command")
	}
	if cfg.AddCommand("go mod download") {
		t.Fatal("AddCommand: want false adding a duplicate")
	}
	if got := cfg.RunCommands(); len(got) != 1 || got[0] != "go mod download" {
		t.Fatalf("RunCommands() = %v, want one entry", got)
	}
}

func TestRemoveCommand(t *testing.T) {
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: ".env"}},
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "go mod download"}},
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "npm install"}},
	}}
	if cfg.RemoveCommand("absent") {
		t.Fatal("RemoveCommand: want false for a command not present")
	}
	if !cfg.RemoveCommand("go mod download") {
		t.Fatal("RemoveCommand: want true removing a present command")
	}
	if got := cfg.RunCommands(); len(got) != 1 || got[0] != "npm install" {
		t.Fatalf("RunCommands() = %v, want only npm install", got)
	}
	// The copy-file step must survive removal of a run-command.
	if len(cfg.Steps) != 2 {
		t.Fatalf("got %d steps, want the copy-file and remaining run-command", len(cfg.Steps))
	}
}

func TestLoadIfPresentMissingFile(t *testing.T) {
	cfg, found, err := loadIfPresent(configPath(t.TempDir()))
	if err != nil {
		t.Fatalf("loadIfPresent: %v", err)
	}
	if found || cfg != nil {
		t.Fatalf("loadIfPresent = (%v, %v), want (nil, false) for a missing file", cfg, found)
	}
}

func TestLoadIfPresentEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(configPath(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, found, err := loadIfPresent(configPath(dir))
	if err != nil {
		t.Fatalf("loadIfPresent: %v", err)
	}
	if !found {
		t.Fatal("loadIfPresent: an empty file is present, not missing")
	}
	if len(cfg.Steps) != 0 {
		t.Fatalf("got %d steps from an empty file, want 0", len(cfg.Steps))
	}
}

func TestLoadIfPresentMalformedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(configPath(dir), []byte("steps:\n  - type: symlink\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, found, err := loadIfPresent(configPath(dir))
	if err == nil {
		t.Fatal("loadIfPresent: want an error for a malformed config, got nil")
	}
	if found {
		t.Error("loadIfPresent: want found=false alongside the error")
	}
	if !strings.Contains(err.Error(), "unknown step type") {
		t.Errorf("error = %q, want it to explain what was malformed", err)
	}
}

func TestLoadIfPresentUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(configPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadIfPresent(configPath(dir))
	if err == nil {
		t.Fatal("loadIfPresent: want an error when the config cannot be read, got nil")
	}
	if !strings.Contains(err.Error(), configPath(dir)) {
		t.Errorf("error = %q, want it to name the unreadable path", err)
	}
}

func TestSaveWriteFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(configPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Save(dir, &Config{})
	if err == nil {
		t.Fatal("Save: want an error when the config path is not writable, got nil")
	}
	if !strings.Contains(err.Error(), "writing "+ConfigFileName) {
		t.Errorf("error = %q, want it to say writing failed", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: ".env"}},
	}}
	cfg.AddCommand("go mod download")
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, found, err := loadIfPresent(configPath(dir))
	if err != nil {
		t.Fatalf("loadIfPresent: %v", err)
	}
	if !found {
		t.Fatal("loadIfPresent: want the saved file to be found")
	}
	if len(got.Steps) != 2 {
		t.Fatalf("got %d steps after round trip, want 2", len(got.Steps))
	}
	if cmds := got.RunCommands(); len(cmds) != 1 || cmds[0] != "go mod download" {
		t.Fatalf("RunCommands() = %v after round trip", cmds)
	}
	// The written file must be parseable on its own terms.
	data, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("Parse of saved file: %v", err)
	}
}

func TestRepoRootFindsGitDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir .git: %v", err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := RepoRoot(sub)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	// Compare resolved paths — t.TempDir may sit under a symlinked /var on macOS.
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Fatalf("RepoRoot(%q) = %q, want %q", sub, got, root)
	}
}

func TestRepoRootErrorsOutsideRepo(t *testing.T) {
	if _, err := RepoRoot(t.TempDir()); err == nil {
		t.Fatal("RepoRoot: want an error outside a git repository")
	}
}
