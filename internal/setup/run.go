package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultCommandTimeout bounds a single run-command step, guarding the
// background sync loop against a hung command.
const DefaultCommandTimeout = 2 * time.Minute

// Options carries the paths and consent state a Run needs.
type Options struct {
	// MainCheckout is the repository's main checkout — copy-file sources
	// resolve against it, so untracked local files (e.g. .env) can be picked up.
	MainCheckout string
	// WorktreeDir is the freshly created worktree — copy-file destinations and
	// run-command's working directory resolve against it.
	WorktreeDir string
	// Consented reports whether the local user has consented to run-command
	// steps for the config being run. When false, run-command steps are
	// skipped (not failed); copy-file steps still run.
	Consented bool
	// CommandTimeout bounds each run-command step; DefaultCommandTimeout is
	// used when zero.
	CommandTimeout time.Duration
}

// Run executes cfg's steps in order against opts, stopping at the first
// failure (fail-fast). It returns the label of the step that failed (see
// Step.Label) and the underlying error; both are zero on success.
func Run(ctx context.Context, cfg *Config, opts Options) (failedStep string, err error) {
	for i, st := range cfg.Steps {
		switch st.Type {
		case StepCopyFile:
			if err := runCopyFile(opts, st.CopyFile); err != nil {
				return st.Label(i), err
			}
		case StepRunCommand:
			if !opts.Consented {
				// The daemon never runs un-consented run-commands; copy-file steps
				// still run (see the caller's consent gating).
				continue
			}
			if err := runCommand(ctx, opts, st.RunCommand); err != nil {
				return st.Label(i), err
			}
		}
	}
	return "", nil
}

// runCopyFile copies one file from the main checkout into the new worktree,
// creating destination directories as needed and preserving the source's mode.
func runCopyFile(opts Options, cf *CopyFile) error {
	src := filepath.Join(opts.MainCheckout, filepath.Clean(cf.Source))
	dst := filepath.Join(opts.WorktreeDir, filepath.Clean(cf.Destination))

	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", cf.Source, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", cf.Source, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("creating destination directory for %s: %w", cf.Destination, err)
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("writing %s: %w", cf.Destination, err)
	}
	return nil
}

// runCommand executes one run-command step in the new worktree, bounded by
// opts.CommandTimeout (DefaultCommandTimeout when unset).
func runCommand(ctx context.Context, opts Options, rc *RunCommand) error {
	timeout := opts.CommandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "sh", "-c", rc.Command)
	cmd.Dir = opts.WorktreeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}
