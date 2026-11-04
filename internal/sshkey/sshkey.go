package sshkey

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

var goos = runtime.GOOS

type Report struct {
	LockedKeys          []string
	UseKeychainDisabled bool
}

func (r Report) Empty() bool {
	return len(r.LockedKeys) == 0 && !r.UseKeychainDisabled
}

type RunFunc func(ctx context.Context, env []string, name string, args ...string) (stdout, stderr string, err error)

type Checker struct {
	Home     string
	Config   string
	Run      RunFunc
	Keychain func(context.Context) ([]string, error)
}

func New() (*Checker, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory: %w", err)
	}
	c := &Checker{Home: home, Run: run}
	c.Keychain = c.keychainKeys
	return c, nil
}

func (c *Checker) Check(ctx context.Context, remoteURL string) (Report, error) {
	if goos != "darwin" {
		return Report{}, nil
	}
	dest, ok := Destination(remoteURL)
	if !ok {
		return Report{}, nil
	}
	identities, err := c.identityFiles(ctx, dest)
	if err != nil {
		return Report{}, err
	}
	var encrypted []string
	for _, path := range identities {
		if c.encrypted(ctx, path) {
			encrypted = append(encrypted, path)
		}
	}
	if len(encrypted) == 0 {
		return Report{}, nil
	}
	stored, err := c.Keychain(ctx)
	if err != nil {
		return Report{}, err
	}
	report := Report{UseKeychainDisabled: !UseKeychainEnabled(c.configPath())}
	for _, path := range encrypted {
		if !slices.ContainsFunc(stored, func(s string) bool { return samePath(s, path) }) {
			report.LockedKeys = append(report.LockedKeys, path)
		}
	}
	return report, nil
}

func Destination(remoteURL string) (string, bool) {
	raw := strings.TrimSpace(remoteURL)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", false
		}
		switch u.Scheme {
		case "ssh", "git+ssh", "ssh+git":
		default:
			return "", false
		}
		if user := u.User.Username(); user != "" {
			return user + "@" + u.Hostname(), true
		}
		return u.Hostname(), true
	}
	colon := strings.Index(raw, ":")
	if colon <= 0 || strings.Contains(raw[:colon], "/") {
		return "", false
	}
	return raw[:colon], true
}

func (c *Checker) identityFiles(ctx context.Context, dest string) ([]string, error) {
	args := []string{"-G"}
	if c.Config != "" {
		args = append(args, "-F", c.Config)
	}
	stdout, stderr, err := c.Run(ctx, nil, "ssh", append(args, dest)...)
	if err != nil {
		return nil, fmt.Errorf("ssh -G %s: %s", dest, firstNonEmpty(strings.TrimSpace(stderr), err.Error()))
	}
	var files []string
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), " ")
		if !ok || key != "identityfile" {
			continue
		}
		path := c.expand(value)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files, nil
}

func (c *Checker) encrypted(ctx context.Context, path string) bool {
	_, stderr, err := c.Run(ctx, nil, "ssh-keygen", "-y", "-P", "", "-f", path)
	return err != nil && strings.Contains(stderr, "incorrect passphrase")
}

func (c *Checker) configPath() string {
	if c.Config != "" {
		return c.Config
	}
	return filepath.Join(c.Home, ".ssh", "config")
}

func (c *Checker) expand(path string) string {
	if path == "~" {
		return c.Home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(c.Home, rest)
	}
	return path
}

func UseKeychainEnabled(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.FieldsFunc(scanner.Text(), func(r rune) bool {
			return r == ' ' || r == '\t' || r == '='
		})
		if len(fields) >= 2 && strings.EqualFold(fields[0], "usekeychain") && strings.EqualFold(fields[1], "yes") {
			return true
		}
	}
	return false
}

func (c *Checker) keychainKeys(ctx context.Context) ([]string, error) {
	dir, err := os.MkdirTemp("", "lj-agent")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "s")

	actx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	agent := exec.CommandContext(actx, "ssh-agent", "-D", "-a", sock)
	if err := agent.Start(); err != nil {
		return nil, fmt.Errorf("starting ssh-agent: %w", err)
	}
	defer func() {
		_ = agent.Process.Kill()
		_ = agent.Wait()
	}()
	if err := waitForSocket(actx, sock); err != nil {
		return nil, err
	}
	stdout, stderr, err := c.Run(actx, []string{"SSH_AUTH_SOCK=" + sock}, "ssh-add", "--apple-load-keychain")
	if err != nil {
		return nil, fmt.Errorf("ssh-add --apple-load-keychain: %s", firstNonEmpty(strings.TrimSpace(stderr), err.Error()))
	}
	return ParseLoaded(stdout + stderr), nil
}

func waitForSocket(ctx context.Context, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("ssh-agent did not start")
		case <-ticker.C:
		}
	}
}

func ParseLoaded(output string) []string {
	var paths []string
	for line := range strings.Lines(output) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Identity added: ")
		if !ok {
			continue
		}
		path, _, _ := strings.Cut(rest, " (")
		paths = append(paths, path)
	}
	return paths
}

func samePath(a, b string) bool {
	return resolve(a) == resolve(b)
}

func resolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func run(ctx context.Context, env []string, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
