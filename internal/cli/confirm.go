package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// Confirm prompts for a yes/no answer on the command's input, defaulting to no.
func Confirm(cmd *cobra.Command, prompt string) bool {
	return ConfirmOn(cmd, cmd.OutOrStdout(), prompt)
}

func ConfirmOn(cmd *cobra.Command, w io.Writer, prompt string) bool {
	_, _ = fmt.Fprintf(w, "%s [y/N] ", prompt)
	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}
