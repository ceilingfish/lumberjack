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
