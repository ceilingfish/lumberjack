package cli

import (
	"os"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Version is the build version reported by `lumberjack daemon` over Health.
// Override at build time with -ldflags "-X github.com/ceilingfish/lumberjack/internal/Version=v1.2.3".
var Version = "dev"

// FormatFlagName is the global output-format flag the root command registers
// and OutputFormat reads back.
const FormatFlagName = "format"

// OutputFormat resolves the effective output format for cmd from the global
// --format flag, gated by whether the real stdout (not the abstract
// io.Writer cmd.OutOrStdout() may wrap in tests) is an interactive terminal,
// and by NO_COLOR.
func OutputFormat(cmd *cobra.Command) (present.Format, error) {
	raw, err := cmd.Flags().GetString(FormatFlagName)
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
