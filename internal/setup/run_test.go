package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCopyFile(t *testing.T) {
	main := t.TempDir()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env
      destination: nested/.env
`))
	if err != nil {
		t.Fatal(err)
	}

	step, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt})
	if err != nil {
		t.Fatalf("Run: %v (step %s)", err, step)
	}
	got, err := os.ReadFile(filepath.Join(wt, "nested", ".env"))
	if err != nil {
		t.Fatalf("reading copied file: %v", err)
	}
	if string(got) != "SECRET=1\n" {
		t.Fatalf("copied content = %q", got)
	}
}

func TestRunCommandSkippedWithoutConsent(t *testing.T) {
	wt := t.TempDir()
	marker := filepath.Join(wt, "ran")
	cfg, err := Parse([]byte(`
steps:
  - type: run-command
    run_command:
      command: touch ran
`))
	if err != nil {
		t.Fatal(err)
	}

	step, err := Run(context.Background(), cfg, Options{MainCheckout: wt, WorktreeDir: wt, Consented: false})
	if err != nil {
		t.Fatalf("Run: %v (step %s)", err, step)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("run-command executed despite no consent")
	}
}

func TestRunCommandRunsWithConsent(t *testing.T) {
	wt := t.TempDir()
	marker := filepath.Join(wt, "ran")
	cfg, err := Parse([]byte(`
steps:
  - type: run-command
    run_command:
      command: touch ran
`))
	if err != nil {
		t.Fatal(err)
	}

	step, err := Run(context.Background(), cfg, Options{MainCheckout: wt, WorktreeDir: wt, Consented: true})
	if err != nil {
		t.Fatalf("Run: %v (step %s)", err, step)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("run-command did not execute despite consent")
	}
}

func TestRunFailFastStopsRemainingSteps(t *testing.T) {
	main := t.TempDir()
	wt := t.TempDir()
	marker := filepath.Join(wt, "should-not-exist")
	cfg, err := Parse([]byte(`
steps:
  - type: copy-file
    copy_file:
      source: missing-file
      destination: dest
  - type: run-command
    run_command:
      command: touch should-not-exist
`))
	if err != nil {
		t.Fatal(err)
	}

	step, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt, Consented: true})
	if err == nil {
		t.Fatal("Run: want error from missing source file, got nil")
	}
	if step != "step 1 (copy-file)" {
		t.Fatalf("failed step = %q, want %q", step, "step 1 (copy-file)")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("later step ran despite earlier failure")
	}
}

func TestRunCommandTimeout(t *testing.T) {
	wt := t.TempDir()
	cfg, err := Parse([]byte(`
steps:
  - type: run-command
    run_command:
      command: sleep 5
`))
	if err != nil {
		t.Fatal(err)
	}

	step, err := Run(context.Background(), cfg, Options{
		MainCheckout: wt, WorktreeDir: wt, Consented: true,
		CommandTimeout: 10 * 1e6, // 10ms
	})
	if err == nil {
		t.Fatal("Run: want timeout error, got nil")
	}
	if step != "step 1 (run-command)" {
		t.Fatalf("failed step = %q", step)
	}
}

// PreserveExisting asks "has the user put something here", so a symlink counts
// as something — following it would clobber a target that may be well outside
// the worktree, and a dangling one is as likely to be deliberate as junk.
func TestRunCopyFilePreservesExistingSymlink(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("from-main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Two destinations: one symlink to a real file elsewhere, one dangling.
	outside := filepath.Join(t.TempDir(), "real.env")
	if err := os.WriteFile(outside, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wt, "linked.env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(wt, "nope"), filepath.Join(wt, "dangling.env")); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: "linked.env"}},
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: "dangling.env"}},
	}}
	if _, err := Run(context.Background(), cfg, Options{
		MainCheckout: main, WorktreeDir: wt, PreserveExisting: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The symlink's target must be untouched, and the link still a link.
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mine\n" {
		t.Errorf("followed the symlink and overwrote its target: %q", got)
	}
	for _, name := range []string{"linked.env", "dangling.env"} {
		fi, err := os.Lstat(filepath.Join(wt, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a symlink; the copy replaced it", name)
		}
	}
}

func TestRunCommandStreamsToOutput(t *testing.T) {
	wt := t.TempDir()
	cfg := &Config{Steps: []Step{
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "echo streamed"}},
	}}
	var buf bytes.Buffer
	if step, err := Run(context.Background(), cfg, Options{
		MainCheckout: wt, WorktreeDir: wt, Consented: true, Output: &buf,
	}); err != nil {
		t.Fatalf("Run: %v (step %s)", err, step)
	}
	if !strings.Contains(buf.String(), "streamed") {
		t.Fatalf("Output = %q, want the command's output streamed to it", buf.String())
	}
}

func TestRunCommandStreamingFailureReportsStep(t *testing.T) {
	wt := t.TempDir()
	cfg := &Config{Steps: []Step{
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "echo boom >&2; exit 3"}},
	}}
	var buf bytes.Buffer
	step, err := Run(context.Background(), cfg, Options{
		MainCheckout: wt, WorktreeDir: wt, Consented: true, Output: &buf,
	})
	if err == nil {
		t.Fatal("Run: want an error from a failing command, got nil")
	}
	if step != "step 1 (run-command)" {
		t.Fatalf("failed step = %q, want step 1 (run-command)", step)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Fatalf("Output = %q, want the failing command's stderr", buf.String())
	}
}

func TestRunCommandFailureWithoutOutputReportsBareError(t *testing.T) {
	wt := t.TempDir()
	cfg := &Config{Steps: []Step{
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "exit 7"}},
	}}
	step, err := Run(context.Background(), cfg, Options{MainCheckout: wt, WorktreeDir: wt, Consented: true})
	if err == nil {
		t.Fatal("Run: want an error from a failing command, got nil")
	}
	if step != "step 1 (run-command)" {
		t.Fatalf("failed step = %q, want step 1 (run-command)", step)
	}
	if got := err.Error(); got != "exit status 7" {
		t.Fatalf("error = %q, want just the exit status when the command printed nothing", got)
	}
}

func TestRunCommandFailureIncludesOutput(t *testing.T) {
	wt := t.TempDir()
	cfg := &Config{Steps: []Step{
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "echo diagnostic; exit 1"}},
	}}
	_, err := Run(context.Background(), cfg, Options{MainCheckout: wt, WorktreeDir: wt, Consented: true})
	if err == nil {
		t.Fatal("Run: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "diagnostic") {
		t.Fatalf("error = %q, want the command's output included", err)
	}
}

func TestRunCommandRunsInWorktreeDir(t *testing.T) {
	wt := t.TempDir()
	cfg := &Config{Steps: []Step{
		{Type: StepRunCommand, RunCommand: &RunCommand{Command: "pwd"}},
	}}
	var buf bytes.Buffer
	if _, err := Run(context.Background(), cfg, Options{
		MainCheckout: t.TempDir(), WorktreeDir: wt, Consented: true, Output: &buf,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("working directory = %q, want the worktree %q", got, want)
	}
}

func TestRunCopyFileUnreadableSource(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(main, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: "adir", Destination: "dest"}},
	}}
	step, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt})
	if err == nil {
		t.Fatal("Run: want an error when the source cannot be read, got nil")
	}
	if step != "step 1 (copy-file)" {
		t.Fatalf("failed step = %q", step)
	}
	if !strings.Contains(err.Error(), "reading adir") {
		t.Fatalf("error = %q, want it to name the unreadable source", err)
	}
}

func TestRunCopyFileDestinationDirectoryCollision(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "blocker"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: "blocker/.env"}},
	}}
	_, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt})
	if err == nil {
		t.Fatal("Run: want an error when the destination directory cannot be created, got nil")
	}
	if !strings.Contains(err.Error(), "creating destination directory") {
		t.Fatalf("error = %q, want it to say the destination directory could not be created", err)
	}
}

func TestRunCopyFileUnwritableDestination(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wt, ".env"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: ".env"}},
	}}
	_, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt})
	if err == nil {
		t.Fatal("Run: want an error when the destination cannot be written, got nil")
	}
	if !strings.Contains(err.Error(), "writing .env") {
		t.Fatalf("error = %q, want it to name the destination it could not write", err)
	}
}

func TestRunCopyFilePreservesSourceMode(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(main, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: "hook.sh", Destination: "hook.sh"}},
	}}
	if _, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fi, err := os.Stat(filepath.Join(wt, "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o755 {
		t.Fatalf("copied mode = %o, want 755", got)
	}
}

func TestRunCopyFileOverwritesWithoutPreserveExisting(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("from-main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".env"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: ".env"}},
	}}
	if _, err := Run(context.Background(), cfg, Options{MainCheckout: main, WorktreeDir: wt}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(wt, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "from-main\n" {
		t.Fatalf("destination = %q, want it overwritten from the main checkout", got)
	}
}

func TestRunCopyFilePreserveExistingStillCopiesWhenAbsent(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("from-main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Steps: []Step{
		{Type: StepCopyFile, CopyFile: &CopyFile{Source: ".env", Destination: ".env"}},
	}}
	if _, err := Run(context.Background(), cfg, Options{
		MainCheckout: main, WorktreeDir: wt, PreserveExisting: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(wt, ".env"))
	if err != nil {
		t.Fatalf("PreserveExisting skipped a copy with nothing at the destination: %v", err)
	}
	if string(got) != "from-main\n" {
		t.Fatalf("destination = %q", got)
	}
}

func TestRunNoStepsSucceeds(t *testing.T) {
	step, err := Run(context.Background(), &Config{}, Options{})
	if err != nil || step != "" {
		t.Fatalf("Run(empty) = (%q, %v), want no failure", step, err)
	}
}
