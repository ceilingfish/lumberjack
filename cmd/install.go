package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// cliBinaryName is the filename the CLI is installed under.
const cliBinaryName = "lumberjack"

// newInstallCmd is the top-level `install`. By default it installs both the
// CLI (copying the running executable to a directory on PATH) and the daemon
// (registering it with the platform service manager). --cli-only / --daemon-only
// narrow it to one half; they are mutually exclusive.
func newInstallCmd() *cobra.Command {
	var daemonOnly, cliOnly, force bool
	var binDir, socketPath string

	c := &cobra.Command{
		Use:   "install",
		Short: "Install the CLI and daemon",
		Long: "Install the CLI to a directory on PATH (default ~/.local/bin) and " +
			"register the daemon with the platform service manager, so it starts " +
			"at login. The daemon is registered against the installed CLI copy — a " +
			"durable path — not whatever binary happens to be running.\n\n" +
			"Must be run from a real built binary, e.g. `mise run install`, or " +
			"`mise build && ./bin/lumberjack install` — a `go run` build is refused " +
			"(see the error for why).\n\n" +
			"--cli-only installs just the CLI copy. --daemon-only registers just the " +
			"daemon, against the installed CLI if present, otherwise the current " +
			"durable binary. Pass --force to reinstall/upgrade an existing install.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolving current executable: %w", err)
			}
			return runInstall(cmd.OutOrStdout(), installOptions{
				exe:        exe,
				binDir:     binDir,
				daemonOnly: daemonOnly,
				cliOnly:    cliOnly,
				force:      force,
				socketPath: socketPath,
			})
		},
	}
	c.Flags().BoolVar(&daemonOnly, "daemon-only", false, "Install only the daemon")
	c.Flags().BoolVar(&cliOnly, "cli-only", false, "Install only the CLI binary")
	c.Flags().BoolVar(&force, "force", false, "Reinstall/upgrade over an existing install")
	c.Flags().StringVar(&binDir, "bin-dir", "",
		"Directory to install the CLI binary into (default ~/.local/bin)")
	c.Flags().StringVar(&socketPath, "socket", "",
		"Unix socket path baked into the installed daemon service (default: ~/.lumberjack/daemon.sock)")
	return c
}

// installOptions is the parsed, validated input to runInstall.
type installOptions struct {
	exe        string // the currently-running executable (os.Executable())
	binDir     string // --bin-dir override; "" means defaultBinDir()
	daemonOnly bool
	cliOnly    bool
	force      bool
	socketPath string
}

// runInstall is the free-function core of `install`, taking the running
// executable path as an argument rather than resolving it itself so it is
// testable without touching the real filesystem/service manager beyond what
// the test explicitly wires up.
func runInstall(out io.Writer, opts installOptions) error {
	if opts.daemonOnly && opts.cliOnly {
		return errors.New("--daemon-only and --cli-only are mutually exclusive")
	}

	binDir := opts.binDir
	if binDir == "" {
		d, err := defaultBinDir()
		if err != nil {
			return err
		}
		binDir = d
	}
	cliPath := filepath.Join(binDir, cliBinaryName)

	var daemonExe string
	if !opts.daemonOnly {
		if err := checkStableExecutable(opts.exe); err != nil {
			return err
		}
		installed, err := installCLI(out, opts.exe, binDir, opts.force)
		if err != nil {
			return err
		}
		daemonExe = installed
		if !binDirOnPath(binDir) {
			if _, err := fmt.Fprintf(out,
				"warning: %s is not on your PATH; add it to run `lumberjack` directly.\n", binDir); err != nil {
				return err
			}
		}
	}

	if !opts.cliOnly {
		if daemonExe == "" {
			_, statErr := os.Stat(cliPath)
			resolved, err := resolveDaemonExecutable(opts.exe, cliPath, statErr == nil)
			if err != nil {
				return err
			}
			daemonExe = resolved
		}
		svc, err := newLifecycle(opts.socketPath, daemonExe)
		if err != nil {
			return err
		}
		if err := installDaemon(out, svc, opts.force); err != nil {
			return err
		}
	}
	return nil
}

// resolveDaemonExecutable picks the binary path the daemon should be
// registered against: the already-installed CLI copy when present (a durable
// path regardless of how `install --daemon-only` itself is being run), or —
// when there is no installed copy — the currently-running executable, subject
// to the go-run guard.
func resolveDaemonExecutable(exe, cliPath string, cliInstalled bool) (string, error) {
	if cliInstalled {
		return cliPath, nil
	}
	if err := checkStableExecutable(exe); err != nil {
		return "", err
	}
	return exe, nil
}

// defaultBinDir is the per-user install location: ~/.local/bin. No sudo is
// required, matching the per-user daemon LaunchAgent.
func defaultBinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// installCLI copies exe into destDir/lumberjack with executable permissions.
// An existing file is left alone unless force is set — install never silently
// clobbers an existing install.
func installCLI(out io.Writer, exe, destDir string, force bool) (string, error) {
	dest := filepath.Join(destDir, cliBinaryName)
	if _, err := os.Stat(dest); err == nil {
		if !force {
			return "", fmt.Errorf("%s already exists; pass --force to overwrite", dest)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking %s: %w", dest, err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", destDir, err)
	}
	if err := copyExecutable(exe, dest); err != nil {
		return "", fmt.Errorf("copying CLI binary: %w", err)
	}
	if _, err := fmt.Fprintf(out, "lumberjack CLI installed to %s\n", dest); err != nil {
		return "", err
	}
	return dest, nil
}

// copyExecutable copies src to dest with executable permissions. It copies to
// a temp file in dest's directory and renames into place rather than writing
// dest directly: overwriting a binary that is currently executing (e.g.
// reinstalling over the installed copy the daemon is running) can fail with
// "text file busy" if opened for writing in place, and a rename is atomic so a
// crash mid-copy never leaves a truncated binary at dest.
func copyExecutable(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".lumberjack-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

// binDirOnPath reports whether dir appears verbatim as an entry of PATH.
func binDirOnPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return true
		}
	}
	return false
}

// checkStableExecutable refuses to install when the running binary is a
// `go run` build. go run links to a transient, content-addressed path under a
// go-build directory: it changes every time the source is rebuilt (a new content
// hash means a new path) and Go prunes the build cache over time. Installing
// from such a path — either copying it as the CLI, or registering the daemon
// against it — leaves durable state pointing at a binary that moves or
// disappears on the next build (the daemon then fails to launch, and under
// KeepAlive, crash-loops). Installing must be done from a durable binary, e.g.
// `mise run install`, or `mise build` then `./bin/lumberjack install`.
func checkStableExecutable(exe string) error {
	if !isEphemeralBuild(exe) {
		return nil
	}
	return fmt.Errorf(
		"refusing to install from a `go run` build (%s): go run builds "+
			"to a transient, content-addressed path that changes on every rebuild "+
			"and is pruned from the build cache over time, so the install "+
			"would soon point at a binary that has moved or gone. Install from a "+
			"durable binary instead:\n\n"+
			"    mise run install\n"+
			"    # or: mise build && ./bin/lumberjack install",
		exe)
}

// isEphemeralBuild reports whether path looks like a binary produced by
// `go run`, which places the compiled executable under a `go-build` directory
// (either a temporary work dir or the persistent build cache). ToSlash
// normalises separators so the match holds on Windows too.
func isEphemeralBuild(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/go-build")
}

// installDaemon registers svc with the service manager and reports next steps.
// When force is set, an existing install is removed first so the registration is
// refreshed with the current binary path and environment — the upgrade path.
func installDaemon(out io.Writer, svc lifecycle, force bool) error {
	if force {
		if err := reinstallDaemon(svc); err != nil {
			return err
		}
	} else if err := svc.Install(); err != nil {
		return fmt.Errorf("installing daemon: %w", err)
	}
	_, err := fmt.Fprintln(out,
		"lumberjack daemon installed; it will start at login. Run `lumberjack daemon start` to start it now.")
	return err
}

// reinstallDaemon removes any existing registration, then installs fresh. The
// service is stopped before removal so an upgrade never leaves an orphaned
// process holding the socket. Missing pieces (not installed, not running) are
// not errors — the goal state is a fresh install regardless of the starting one.
func reinstallDaemon(svc lifecycle) error {
	// Stop is best-effort: a not-running service errors here on most platforms,
	// and Uninstall unloads it anyway. What matters is that Install succeeds.
	_ = svc.Stop()
	if err := svc.Uninstall(); err != nil && !errors.Is(err, service.ErrNotInstalled) {
		return fmt.Errorf("removing existing daemon for reinstall: %w", err)
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("installing daemon: %w", err)
	}
	return nil
}
