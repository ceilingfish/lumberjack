// Package cmd holds the Cobra command tree. Files here parse flags and delegate
// immediately — CLI commands to the gRPC client in pkg/client, the daemon
// command to internal/daemon. No business logic lives here (AGENTS.md).
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is the build version reported by `lumberjack daemon` over Health.
// Override at build time with -ldflags "-X github.com/ceilingfish/lumberjack/cmd.version=v1.2.3".
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "lumberjack",
		Short:         "Track open PRs and reconcile git worktrees",
		SilenceUsage:  true, // don't dump usage on a returned RunE error
		SilenceErrors: true, // Execute prints the error itself, once
	}
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newRepositoriesCmd())
	root.AddCommand(newRepositoryCmd())
	root.AddCommand(newSyncCmd())
	return root
}

// Execute runs the root command. main.go calls nothing else.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "lumberjack:", err)
		os.Exit(1)
	}
}
