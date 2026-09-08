package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// BinaryName is the filename the CLI is installed under.
const BinaryName = "lumberjack"

// DefaultBinDir is the per-user install location: ~/.local/bin. No sudo is
// required, matching the per-user daemon LaunchAgent.
func DefaultBinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}
