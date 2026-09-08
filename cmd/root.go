// Package cmd holds the Cobra command tree. Files here parse flags and delegate
// immediately — CLI commands to the gRPC client in pkg/client, the daemon
// command to internal/daemon. No business logic lives here (AGENTS.md).
package cmd

import (
	"fmt"
	"os"

	"github.com/ceilingfish/lumberjack/internal/cli"
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "lumberjack",
		Short:         "Track open PRs and reconcile git worktrees",
		SilenceUsage:  true, // don't dump usage on a returned RunE error
		SilenceErrors: true, // Execute prints the error itself, once
	}
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newDeleteCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newInstallCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newSetLoginCmd())
	root.AddCommand(newSetupCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newSyncAllCmd())
	root.AddCommand(newTidyCmd())
	root.AddCommand(newUninstallCmd())
	root.AddCommand(newWorktreeCmd())
	root.AddCommand(newWorktreesCmd())
	root.PersistentFlags().String(cli.FormatFlagName, "",
		"output format: color, structured, or json (default: color on an interactive "+
			"terminal with NO_COLOR unset, structured otherwise)")
	return root
}

// Execute runs the root command. main.go calls nothing else.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "lumberjack:", err)
		os.Exit(1)
	}
}
