package setup

import (
	"context"
	"os"
	"path/filepath"
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
