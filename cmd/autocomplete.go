package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/present"
)

const completionMarker = "lumberjack completion"

var completionLines = map[string]string{
	"bash":       `eval "$(lumberjack completion bash)"`,
	"fish":       "lumberjack completion fish | source",
	"powershell": "lumberjack completion powershell | Out-String | Invoke-Expression",
	"zsh":        `eval "$(lumberjack completion zsh)"`,
}

func completionShellValues() []string {
	names := make([]string, 0, len(completionLines))
	for name := range completionLines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var parentProcessName = func() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(os.Getppid())).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func normaliseShellName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}
	name = filepath.Base(filepath.ToSlash(name))
	name = strings.TrimPrefix(name, "-")
	name = strings.ToLower(strings.TrimSuffix(name, ".exe"))
	if name == "pwsh" {
		name = "powershell"
	}
	if _, ok := completionLines[name]; !ok {
		return ""
	}
	return name
}

func detectShell(parent, shellEnv string) string {
	if name := normaliseShellName(parent); name != "" {
		return name
	}
	return normaliseShellName(shellEnv)
}

func completionRCPath(shell, goos, home string, exists func(string) bool) (string, error) {
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	case "powershell":
		return powershellProfilePath(goos, home), nil
	case "bash":
		return bashRCPath(goos, home, exists), nil
	default:
		return "", fmt.Errorf("unsupported shell %q: want one of %v", shell, completionShellValues())
	}
}

func bashRCPath(goos, home string, exists func(string) bool) string {
	profile := filepath.Join(home, ".bash_profile")
	rc := filepath.Join(home, ".bashrc")
	candidates := []string{rc, profile}
	if goos == "darwin" {
		candidates = []string{profile, rc}
	}
	for _, c := range candidates {
		if exists(c) {
			return c
		}
	}
	return rc
}

func powershellProfilePath(goos, home string) string {
	if p := strings.TrimSpace(os.Getenv("PROFILE")); p != "" {
		return p
	}
	if goos == "windows" {
		return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	}
	return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func completionCandidateRCPaths(goos, home string) []string {
	paths := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
		powershellProfilePath(goos, home),
	}
	seen := make(map[string]bool, len(paths))
	unique := paths[:0]
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		unique = append(unique, p)
	}
	return unique
}

func rcMentionsCompletion(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, completionMarker) && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			return true, nil
		}
	}
	return false, nil
}

func appendCompletionLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var b strings.Builder
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString(line)
	b.WriteString("\n")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func removeCompletionLines(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		if strings.Contains(line, completionMarker) && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return false, nil
	}
	info, err := os.Stat(path)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	return true, os.WriteFile(path, []byte(strings.Join(kept, "\n")), mode)
}

func completionColorEnabled() bool {
	_, noColorSet := os.LookupEnv("NO_COLOR")
	return present.ColorGate(interactiveTerminal(), noColorSet)
}

func warnCompletion(errOut io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintln(errOut, present.StatusWarn(fmt.Sprintf(format, args...), completionColorEnabled()))
}

func manualCompletionInstruction(errOut io.Writer, rc, line string) {
	if rc == "" {
		warnCompletion(errOut,
			"shell completion was not configured; add the line for your shell from `lumberjack completion --help` to your shell rc file.")
		return
	}
	warnCompletion(errOut, "shell completion was not configured; add this line to %s by hand:\n    %s", rc, line)
}

var completionConfirmer = confirmCompletion

func confirmCompletion(errOut io.Writer, rc, line string) (bool, error) {
	in, restore, err := rawTerminal()
	if err != nil {
		return false, err
	}
	defer restore()
	_, _ = fmt.Fprintf(errOut,
		"Add shell completion to %s?\r\n    %s\r\n  [Y] yes  [n] no\r\n", rc, line)
	buf := make([]byte, 3)
	for {
		n, err := in.Read(buf)
		if err != nil {
			return false, err
		}
		if n == 0 {
			continue
		}
		switch buf[0] {
		case 'y', 'Y', '\r', '\n':
			return true, nil
		case 'n', 'N', 0x03:
			return false, nil
		}
	}
}

func installCompletion(out, errOut io.Writer, opts installOptions) {
	if opts.daemonOnly || opts.noAutocomplete {
		return
	}
	shell := opts.autocompleteShell
	ask := false
	if shell == "" {
		if !interactiveTerminal() {
			return
		}
		shell = detectShell(parentProcessName(), os.Getenv("SHELL"))
		if shell == "" {
			manualCompletionInstruction(errOut, "", "")
			return
		}
		ask = true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		warnCompletion(errOut, "resolving home directory for shell completion: %v", err)
		return
	}
	rc, err := completionRCPath(shell, runtime.GOOS, home, fileExists)
	if err != nil {
		warnCompletion(errOut, "%v", err)
		return
	}
	line := completionLines[shell]

	already, err := rcMentionsCompletion(rc)
	if err != nil {
		warnCompletion(errOut, "reading %s: %v", rc, err)
		manualCompletionInstruction(errOut, rc, line)
		return
	}
	if already {
		return
	}

	if ask {
		ok, err := completionConfirmer(errOut, rc, line)
		if err != nil {
			manualCompletionInstruction(errOut, rc, line)
			return
		}
		if !ok {
			return
		}
	}

	if err := appendCompletionLine(rc, line); err != nil {
		warnCompletion(errOut, "writing %s: %v", rc, err)
		manualCompletionInstruction(errOut, rc, line)
		return
	}
	_, _ = fmt.Fprintf(out,
		"shell completion added to %s; run `source %s` or open a new shell to use it.\n", rc, rc)
}

func uninstallCompletion(out, errOut io.Writer, daemonOnly bool) {
	if daemonOnly {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		warnCompletion(errOut, "resolving home directory for shell completion: %v", err)
		return
	}

	var found []string
	for _, rc := range completionCandidateRCPaths(runtime.GOOS, home) {
		has, err := rcMentionsCompletion(rc)
		if err != nil {
			warnCompletion(errOut, "reading %s: %v", rc, err)
			continue
		}
		if has {
			found = append(found, rc)
		}
	}
	if len(found) == 0 {
		return
	}

	if !interactiveTerminal() {
		warnCompletion(errOut,
			"these files still source lumberjack shell completion; remove the line by hand:\n    %s",
			strings.Join(found, "\n    "))
		return
	}

	for _, rc := range found {
		ok, err := completionConfirmer(errOut, rc, "remove the lumberjack completion line")
		if err != nil {
			warnCompletion(errOut, "prompting about %s: %v", rc, err)
			return
		}
		if !ok {
			continue
		}
		removed, err := removeCompletionLines(rc)
		if err != nil {
			warnCompletion(errOut, "editing %s: %v", rc, err)
			continue
		}
		if removed {
			_, _ = fmt.Fprintf(out, "shell completion removed from %s\n", rc)
		}
	}
}

var errAutocompleteExclusive = errors.New(
	"--autocomplete-shell and --no-autocomplete are mutually exclusive")

func validateAutocompleteOptions(shell string, disabled bool) error {
	if shell != "" && disabled {
		return errAutocompleteExclusive
	}
	if shell == "" {
		return nil
	}
	if _, ok := completionLines[shell]; !ok {
		return fmt.Errorf("invalid --autocomplete-shell %q: want one of %v", shell, completionShellValues())
	}
	return nil
}
