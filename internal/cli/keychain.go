package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

const useKeychainBlock = "Host *\n  UseKeychain yes\n  AddKeysToAgent yes\n"

var SSHAdd = func(cmd *cobra.Command, path string) error {
	c := exec.CommandContext(cmd.Context(), "ssh-add", "--apple-use-keychain", path)
	c.Stdin = os.Stdin
	c.Stdout = cmd.ErrOrStderr()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

var SSHConfigPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func PromptSSHKeychain(cmd *cobra.Command, interactive bool, checks ...*lumberjackv1.SshKeychainCheck) error {
	var locked []string
	disabled := false
	for _, c := range checks {
		for _, k := range c.GetLockedKeys() {
			if !slices.Contains(locked, k) {
				locked = append(locked, k)
			}
		}
		disabled = disabled || c.GetUseKeychainDisabled()
	}
	if len(locked) == 0 && !disabled {
		return nil
	}

	w := cmd.ErrOrStderr()
	configPath, err := SSHConfigPath()
	if err != nil {
		return err
	}
	if err := describeSSHKeychain(w, locked, disabled, configPath); err != nil {
		return err
	}
	if !interactive || !InteractiveTerminal() || !ConfirmOn(cmd, w, "Run these now?") {
		return nil
	}
	for _, k := range locked {
		if err := SSHAdd(cmd, k); err != nil {
			_, _ = fmt.Fprintf(w, "warning: ssh-add %s failed: %v\n", k, err)
		}
	}
	if disabled {
		if err := appendUseKeychain(configPath); err != nil {
			_, _ = fmt.Fprintf(w, "warning: updating %s failed: %v\n", configPath, err)
			return nil
		}
		_, _ = fmt.Fprintf(w, "Added UseKeychain to %s\n", configPath)
	}
	return nil
}

func describeSSHKeychain(w io.Writer, locked []string, disabled bool, configPath string) error {
	if len(locked) > 0 {
		if _, err := fmt.Fprintln(w, "These SSH keys are passphrase-protected, but their passphrase is not in the macOS Keychain, so the daemon cannot unlock them to fetch:"); err != nil {
			return err
		}
		for _, k := range locked {
			if _, err := fmt.Fprintf(w, "  %s\n", k); err != nil {
				return err
			}
		}
	}
	if disabled {
		if _, err := fmt.Fprintf(w, "%s does not enable UseKeychain, so ssh will not read stored passphrases once the agent is empty (e.g. after a reboot).\n", configPath); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "To fix, run:"); err != nil {
		return err
	}
	for _, k := range locked {
		if _, err := fmt.Fprintf(w, "  ssh-add --apple-use-keychain %s\n", k); err != nil {
			return err
		}
	}
	if disabled {
		if _, err := fmt.Fprintf(w, "  printf '\\n%s' >> %s\n", strings.ReplaceAll(useKeychainBlock, "\n", `\n`), configPath); err != nil {
			return err
		}
	}
	return nil
}

func appendUseKeychain(configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("\n" + useKeychainBlock); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
