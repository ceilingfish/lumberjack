package sshkey

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func onDarwin(t *testing.T) {
	t.Helper()
	prev := goos
	goos = "darwin"
	t.Cleanup(func() { goos = prev })
}

type fakeHost struct {
	identities []string
	encrypted  map[string]bool
	gErr       error
	gStderr    string
	calls      [][]string
}

func (f *fakeHost) run(_ context.Context, _ []string, name string, args ...string) (string, string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch name {
	case "ssh":
		if f.gErr != nil {
			return "", f.gStderr, f.gErr
		}
		var b strings.Builder
		b.WriteString("user git\n")
		for _, id := range f.identities {
			b.WriteString("identityfile " + id + "\n")
		}
		return b.String(), "", nil
	case "ssh-keygen":
		if f.encrypted[args[len(args)-1]] {
			return "", "Load key: incorrect passphrase supplied to decrypt private key", errors.New("exit status 255")
		}
		return "ssh-ed25519 AAAA\n", "", nil
	}
	return "", "", errors.New("unexpected command " + name)
}

func writeKey(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newChecker(t *testing.T, host *fakeHost, stored []string) *Checker {
	t.Helper()
	return &Checker{
		Home:     t.TempDir(),
		Run:      host.run,
		Keychain: func(context.Context) ([]string, error) { return stored, nil },
	}
}

func TestCheckReportsEncryptedKeysMissingFromTheKeychain(t *testing.T) {
	onDarwin(t)
	host := &fakeHost{encrypted: map[string]bool{}}
	c := newChecker(t, host, nil)
	sshDir := filepath.Join(c.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeKey(t, sshDir, "id_rsa")
	locked := writeKey(t, sshDir, "id_ed25519")
	stored := writeKey(t, sshDir, "id_stored")
	host.identities = []string{"~/.ssh/id_rsa", "~/.ssh/id_ed25519", stored, "~/.ssh/id_missing"}
	host.encrypted[locked] = true
	host.encrypted[stored] = true
	c.Keychain = func(context.Context) ([]string, error) { return []string{stored}, nil }

	got, err := c.Check(context.Background(), "git@github.com:o/n.git")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !slices.Equal(got.LockedKeys, []string{locked}) {
		t.Errorf("LockedKeys = %v, want [%s]", got.LockedKeys, locked)
	}
	if !got.UseKeychainDisabled {
		t.Error("UseKeychainDisabled = false, want true with no ssh config")
	}
	if want := []string{"ssh", "-G", "git@github.com"}; !slices.Equal(host.calls[0], want) {
		t.Errorf("first call = %v, want %v", host.calls[0], want)
	}
}

func TestCheckHonoursUseKeychainInTheConfig(t *testing.T) {
	onDarwin(t)
	host := &fakeHost{encrypted: map[string]bool{}}
	c := newChecker(t, host, nil)
	key := writeKey(t, c.Home, "id")
	host.identities = []string{key}
	host.encrypted[key] = true
	c.Config = filepath.Join(c.Home, "config")
	if err := os.WriteFile(c.Config, []byte("Host *\n  UseKeychain yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := c.Check(context.Background(), "ssh://git@github.com/o/n")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.UseKeychainDisabled || !slices.Equal(got.LockedKeys, []string{key}) {
		t.Errorf("Check = %+v", got)
	}
	if want := []string{"ssh", "-G", "-F", c.Config, "git@github.com"}; !slices.Equal(host.calls[0], want) {
		t.Errorf("first call = %v, want %v", host.calls[0], want)
	}
}

func TestCheckIsEmptyWithoutEncryptedKeys(t *testing.T) {
	onDarwin(t)
	host := &fakeHost{}
	c := newChecker(t, host, nil)
	host.identities = []string{writeKey(t, c.Home, "id")}
	c.Keychain = func(context.Context) ([]string, error) {
		t.Error("Keychain consulted with no encrypted keys")
		return nil, nil
	}

	got, err := c.Check(context.Background(), "git@github.com:o/n.git")
	if err != nil || !got.Empty() {
		t.Errorf("Check = %+v, %v; want empty", got, err)
	}
}

func TestCheckSkipsNonSSHRemotesAndOtherPlatforms(t *testing.T) {
	host := &fakeHost{}
	c := newChecker(t, host, nil)

	onDarwin(t)
	if got, err := c.Check(context.Background(), "https://github.com/o/n.git"); err != nil || !got.Empty() {
		t.Errorf("https Check = %+v, %v", got, err)
	}
	goos = "linux"
	if got, err := c.Check(context.Background(), "git@github.com:o/n.git"); err != nil || !got.Empty() {
		t.Errorf("linux Check = %+v, %v", got, err)
	}
	if len(host.calls) != 0 {
		t.Errorf("calls = %v, want none", host.calls)
	}
}

func TestCheckSurfacesFailures(t *testing.T) {
	onDarwin(t)

	host := &fakeHost{gErr: errors.New("exit status 255"), gStderr: "Bad configuration option"}
	if _, err := newChecker(t, host, nil).Check(context.Background(), "git@h:o/n"); err == nil || !strings.Contains(err.Error(), "Bad configuration option") {
		t.Errorf("ssh -G failure err = %v", err)
	}

	host = &fakeHost{encrypted: map[string]bool{}}
	c := newChecker(t, host, nil)
	key := writeKey(t, c.Home, "id")
	host.identities = []string{key}
	host.encrypted[key] = true
	c.Keychain = func(context.Context) ([]string, error) { return nil, errors.New("no agent") }
	if _, err := c.Check(context.Background(), "git@h:o/n"); err == nil || err.Error() != "no agent" {
		t.Errorf("keychain failure err = %v", err)
	}
}

func TestDestination(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"git@github.com:o/n.git", "git@github.com", true},
		{"github.com-personal:o/n", "github.com-personal", true},
		{"ssh://git@github.com:2222/o/n", "git@github.com", true},
		{"git+ssh://github.com/o/n", "github.com", true},
		{"https://github.com/o/n.git", "", false},
		{"ssh://", "", false},
		{"/srv/repo.git", "", false},
		{"./a:b", "", false},
		{"", "", false},
	} {
		got, ok := Destination(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Destination(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUseKeychainEnabled(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		config string
		want   bool
	}{
		{"Host *\n  UseKeychain yes\n", true},
		{"usekeychain=YES\n", true},
		{"# UseKeychain yes\n", false},
		{"UseKeychain no\n", false},
		{"Host x\n  IdentityFile ~/.ssh/id\n", false},
	} {
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := UseKeychainEnabled(path); got != tc.want {
			t.Errorf("UseKeychainEnabled(%q) = %v, want %v", tc.config, got, tc.want)
		}
	}
	if UseKeychainEnabled(filepath.Join(dir, "missing")) {
		t.Error("UseKeychainEnabled(missing) = true")
	}
}

func TestParseLoaded(t *testing.T) {
	out := "Identity added: /Users/me/.ssh/id_ed25519 (me@host)\nNo identity found in the keychain.\nIdentity added: /a b/key (c)\n"
	if got, want := ParseLoaded(out), []string{"/Users/me/.ssh/id_ed25519", "/a b/key"}; !slices.Equal(got, want) {
		t.Errorf("ParseLoaded = %v, want %v", got, want)
	}
}

func TestExpand(t *testing.T) {
	c := &Checker{Home: "/home/me"}
	for in, want := range map[string]string{"~": "/home/me", "~/.ssh/id": "/home/me/.ssh/id", "/abs": "/abs"} {
		if got := c.expand(in); got != want {
			t.Errorf("expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSamePathFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	key := writeKey(t, dir, "id")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	if !samePath(link, key) || samePath(key, filepath.Join(dir, "other")) {
		t.Error("samePath did not resolve the symlink")
	}
}

func TestNew(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Home == "" || c.Run == nil || c.Keychain == nil {
		t.Errorf("New = %+v", c)
	}
	if _, stderr, err := c.Run(context.Background(), []string{"X=1"}, "sh", "-c", "echo $X >&2"); err != nil || strings.TrimSpace(stderr) != "1" {
		t.Errorf("run = %q, %v", stderr, err)
	}
}

func TestKeychainKeysLoadsIntoAThrowawayAgent(t *testing.T) {
	if _, err := exec.LookPath("ssh-agent"); err != nil {
		t.Skip("ssh-agent not installed")
	}
	var env []string
	c := &Checker{Run: func(_ context.Context, e []string, name string, args ...string) (string, string, error) {
		env = e
		if name != "ssh-add" || !slices.Equal(args, []string{"--apple-load-keychain"}) {
			t.Errorf("ran %s %v", name, args)
		}
		return "", "Identity added: /k (c)\n", nil
	}}

	got, err := c.keychainKeys(context.Background())
	if err != nil {
		t.Fatalf("keychainKeys: %v", err)
	}
	if !slices.Equal(got, []string{"/k"}) {
		t.Errorf("keychainKeys = %v", got)
	}
	if len(env) != 1 || !strings.HasPrefix(env[0], "SSH_AUTH_SOCK=") {
		t.Errorf("env = %v", env)
	}
	if _, err := os.Stat(strings.TrimPrefix(env[0], "SSH_AUTH_SOCK=")); !os.IsNotExist(err) {
		t.Errorf("agent socket left behind: %v", err)
	}
}

func TestKeychainKeysSurfacesSSHAddFailure(t *testing.T) {
	if _, err := exec.LookPath("ssh-agent"); err != nil {
		t.Skip("ssh-agent not installed")
	}
	c := &Checker{Run: func(context.Context, []string, string, ...string) (string, string, error) {
		return "", "unknown option -- -", errors.New("exit status 1")
	}}
	if _, err := c.keychainKeys(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("keychainKeys err = %v", err)
	}
}
