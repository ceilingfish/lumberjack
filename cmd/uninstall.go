package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// newUninstallCmd is the top-level `uninstall`. By default it reverses
// `install`: stops and deregisters the daemon, and removes the installed CLI
// binary. --cli-only / --daemon-only narrow it to one half; they are mutually
// exclusive.
func newUninstallCmd() *cobra.Command {
	var daemonOnly, cliOnly bool
	var binDir string

	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the CLI and daemon",
		Long: "Stop and deregister the daemon from the platform service manager, " +
			"and remove the installed CLI binary. --cli-only / --daemon-only scope " +
			"this to just one of the two.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd.OutOrStdout(), uninstallOptions{
				binDir:     binDir,
				daemonOnly: daemonOnly,
				cliOnly:    cliOnly,
			})
		},
	}
	c.Flags().BoolVar(&daemonOnly, "daemon-only", false, "Uninstall only the daemon")
	c.Flags().BoolVar(&cliOnly, "cli-only", false, "Uninstall only the CLI binary")
	c.Flags().StringVar(&binDir, "bin-dir", "",
		"Directory the CLI binary was installed into (default ~/.local/bin)")
	return c
}

// uninstallOptions is the parsed, validated input to runUninstall.
type uninstallOptions struct {
	binDir     string // --bin-dir override; "" means defaultBinDir()
	daemonOnly bool
	cliOnly    bool
}

func runUninstall(out io.Writer, opts uninstallOptions) error {
	if opts.daemonOnly && opts.cliOnly {
		return errors.New("--daemon-only and --cli-only are mutually exclusive")
	}

	if !opts.cliOnly {
		svc, err := newLifecycle("", "")
		if err != nil {
			return err
		}
		if err := uninstallDaemon(out, svc); err != nil {
			return err
		}
	}

	if !opts.daemonOnly {
		binDir := opts.binDir
		if binDir == "" {
			d, err := defaultBinDir()
			if err != nil {
				return err
			}
			binDir = d
		}
		if err := uninstallCLI(out, binDir); err != nil {
			return err
		}
	}
	return nil
}

// uninstallDaemon stops and deregisters svc. A not-installed daemon is not an
// error — uninstall's goal state (no registration) is already met.
func uninstallDaemon(out io.Writer, svc lifecycle) error {
	_ = svc.Stop()
	if err := svc.Uninstall(); err != nil && !errors.Is(err, service.ErrNotInstalled) {
		return fmt.Errorf("uninstalling daemon: %w", err)
	}
	_, err := fmt.Fprintln(out, "lumberjack daemon uninstalled.")
	return err
}

// uninstallCLI removes the installed CLI binary from binDir. A missing binary
// is reported, not an error — uninstall's goal state is already met.
func uninstallCLI(out io.Writer, binDir string) error {
	dest := filepath.Join(binDir, cliBinaryName)
	if err := os.Remove(dest); err != nil {
		if os.IsNotExist(err) {
			_, werr := fmt.Fprintf(out, "lumberjack CLI not found at %s; nothing to remove.\n", dest)
			return werr
		}
		return fmt.Errorf("removing %s: %w", dest, err)
	}
	_, err := fmt.Fprintf(out, "lumberjack CLI removed from %s\n", dest)
	return err
}
