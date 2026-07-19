// Package cmd holds the Cobra command tree. Files here parse flags and delegate
// immediately — CLI commands to the gRPC client in pkg/client, the daemon
// command to internal/daemon. No business logic lives here (AGENTS.md).
package cmd

import (
	"fmt"
	"os"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
	root.AddCommand(newDeleteCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newRepositoriesCmd())
	root.AddCommand(newSetLoginCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newSyncCmd())
	root.PersistentFlags().String("format", "",
		"output format: color, structured, or json (default: color on an interactive "+
			"terminal with NO_COLOR unset, structured otherwise)")
	return root
}

// outputFormat resolves the effective output format for cmd from the global
// --format flag, gated by whether the real stdout (not the abstract
// io.Writer cmd.OutOrStdout() may wrap in tests) is an interactive terminal,
// and by NO_COLOR.
func outputFormat(cmd *cobra.Command) (present.Format, error) {
	raw, err := cmd.Flags().GetString("format")
	if err != nil {
		return "", err
	}
	var explicit present.Format
	if raw != "" {
		explicit, err = present.Parse(raw)
		if err != nil {
			return "", err
		}
	}
	_, noColorSet := os.LookupEnv("NO_COLOR")
	isTerminal := term.IsTerminal(int(os.Stdout.Fd()))
	return present.Resolve(explicit, present.ColorGate(isTerminal, noColorSet)), nil
}

// Execute runs the root command. main.go calls nothing else.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "lumberjack:", err)
		os.Exit(1)
	}
}
