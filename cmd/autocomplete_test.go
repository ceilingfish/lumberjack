package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeInteractive(t *testing.T, v bool) {
	t.Helper()
	prev := interactiveTerminal
	interactiveTerminal = func() bool { return v }
	t.Cleanup(func() { interactiveTerminal = prev })
}

func fakeParent(t *testing.T, name string) {
	t.Helper()
	prev := parentProcessName
	parentProcessName = func() string { return name }
	t.Cleanup(func() { parentProcessName = prev })
}

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PROFILE", filepath.Join(home, "profile.ps1"))
	return home
}

func scriptConfirm(t *testing.T, answers ...bool) *[]string {
	t.Helper()
	var asked []string
	prev := completionConfirmer
	completionConfirmer = func(_ io.Writer, rc, _ string) (bool, error) {
		asked = append(asked, rc)
		if len(answers) == 0 {
			return false, nil
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	t.Cleanup(func() { completionConfirmer = prev })
	return &asked
}

func failConfirm(t *testing.T, err error) {
	t.Helper()
	prev := completionConfirmer
	completionConfirmer = func(io.Writer, string, string) (bool, error) { return false, err }
	t.Cleanup(func() { completionConfirmer = prev })
}

func TestDetectShellPrefersParentProcess(t *testing.T) {
	cases := []struct {
		parent, env, want string
	}{
		{"-zsh", "/bin/bash", "zsh"},
		{"/usr/local/bin/fish", "/bin/zsh", "fish"},
		{"pwsh.exe", "", "powershell"},
		{"powershell", "", "powershell"},
		{"", "/bin/bash", "bash"},
		{"login", "/opt/homebrew/bin/zsh", "zsh"},
		{"tmux", "/usr/bin/csh", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := detectShell(c.parent, c.env); got != c.want {
			t.Errorf("detectShell(%q, %q) = %q, want %q", c.parent, c.env, got, c.want)
		}
	}
}

func TestCompletionRCPathMapping(t *testing.T) {
	home := "/home/u"
	none := func(string) bool { return false }
	cases := []struct {
		shell, goos, want string
	}{
		{"zsh", "linux", filepath.Join(home, ".zshrc")},
		{"fish", "darwin", filepath.Join(home, ".config", "fish", "config.fish")},
		{"bash", "darwin", filepath.Join(home, ".bashrc")},
		{"bash", "linux", filepath.Join(home, ".bashrc")},
	}
	for _, c := range cases {
		got, err := completionRCPath(c.shell, c.goos, home, none)
		if err != nil {
			t.Fatalf("completionRCPath(%q): %v", c.shell, err)
		}
		if got != c.want {
			t.Errorf("completionRCPath(%q, %q) = %q, want %q", c.shell, c.goos, got, c.want)
		}
	}
	if _, err := completionRCPath("csh", "linux", home, none); err == nil {
		t.Error("expected an error for an unsupported shell")
	}
}

func TestBashRCPathPrefersExistingFile(t *testing.T) {
	home := "/home/u"
	profile := filepath.Join(home, ".bash_profile")
	rc := filepath.Join(home, ".bashrc")
	only := func(p string) func(string) bool {
		return func(q string) bool { return q == p }
	}
	both := func(string) bool { return true }

	if got := bashRCPath("darwin", home, both); got != profile {
		t.Errorf("darwin with both = %q, want %q", got, profile)
	}
	if got := bashRCPath("linux", home, both); got != rc {
		t.Errorf("linux with both = %q, want %q", got, rc)
	}
	if got := bashRCPath("darwin", home, only(rc)); got != rc {
		t.Errorf("darwin with only .bashrc = %q, want %q", got, rc)
	}
	if got := bashRCPath("linux", home, only(profile)); got != profile {
		t.Errorf("linux with only .bash_profile = %q, want %q", got, profile)
	}
}

func TestPowershellProfilePath(t *testing.T) {
	t.Setenv("PROFILE", "/somewhere/profile.ps1")
	if got := powershellProfilePath("linux", "/home/u"); got != "/somewhere/profile.ps1" {
		t.Errorf("$PROFILE not honoured: %q", got)
	}
	t.Setenv("PROFILE", "")
	if got := powershellProfilePath("windows", `C:\Users\u`); !strings.Contains(got, "Microsoft.PowerShell_profile.ps1") {
		t.Errorf("windows profile = %q", got)
	}
	if got := powershellProfilePath("linux", "/home/u"); got != "/home/u/.config/powershell/Microsoft.PowerShell_profile.ps1" {
		t.Errorf("linux profile = %q", got)
	}
}

func TestValidateAutocompleteOptions(t *testing.T) {
	if err := validateAutocompleteOptions("zsh", true); !errors.Is(err, errAutocompleteExclusive) {
		t.Errorf("mutual exclusion not enforced: %v", err)
	}
	if err := validateAutocompleteOptions("", true); err != nil {
		t.Errorf("--no-autocomplete alone: %v", err)
	}
	if err := validateAutocompleteOptions("csh", false); err == nil {
		t.Error("expected an error for an unknown shell")
	}
	if err := validateAutocompleteOptions("fish", false); err != nil {
		t.Errorf("fish: %v", err)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if fileExists(p) {
		t.Error("missing file reported as existing")
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileExists(p) {
		t.Error("existing file reported as missing")
	}
}

func TestRCMentionsCompletion(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	has, err := rcMentionsCompletion(missing)
	if err != nil || has {
		t.Errorf("missing file = (%v, %v), want (false, nil)", has, err)
	}

	commented := filepath.Join(dir, "commented")
	if err := os.WriteFile(commented, []byte("# eval \"$(lumberjack completion zsh)\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if has, _ := rcMentionsCompletion(commented); has {
		t.Error("a commented-out line should not count")
	}

	live := filepath.Join(dir, "live")
	if err := os.WriteFile(live, []byte("export A=1\neval \"$(lumberjack completion zsh)\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if has, _ := rcMentionsCompletion(live); !has {
		t.Error("a live line should count")
	}

	if _, err := rcMentionsCompletion(dir); err == nil {
		t.Error("expected an error reading a directory")
	}
}

func TestAppendCompletionLineAddsMissingNewline(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, "nested", "rc")
	if err := appendCompletionLine(rc, "line-one"); err != nil {
		t.Fatalf("appendCompletionLine: %v", err)
	}
	if err := os.WriteFile(rc, []byte("export A=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendCompletionLine(rc, "line-two"); err != nil {
		t.Fatalf("appendCompletionLine: %v", err)
	}
	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "export A=1\nline-two\n" {
		t.Errorf("rc = %q", got)
	}
	if err := appendCompletionLine(filepath.Join(dir, "nested", "rc", "deeper"), "x"); err == nil {
		t.Error("expected an error appending under a file")
	}
}

func TestRemoveCompletionLines(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, "rc")
	body := "export A=1\neval \"$(lumberjack completion zsh)\"\nexport B=2\n"
	if err := os.WriteFile(rc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := removeCompletionLines(rc)
	if err != nil || !removed {
		t.Fatalf("removeCompletionLines = (%v, %v)", removed, err)
	}
	got, _ := os.ReadFile(rc)
	if strings.Contains(string(got), completionMarker) {
		t.Errorf("rc still mentions completion: %q", got)
	}
	if !strings.Contains(string(got), "export B=2") {
		t.Errorf("unrelated lines were dropped: %q", got)
	}
	if removed, _ := removeCompletionLines(rc); removed {
		t.Error("second removal should report nothing removed")
	}
	if removed, err := removeCompletionLines(filepath.Join(dir, "missing")); removed || err != nil {
		t.Errorf("missing file = (%v, %v)", removed, err)
	}
	if _, err := removeCompletionLines(dir); err == nil {
		t.Error("expected an error reading a directory")
	}
}

func TestCompletionCandidateRCPathsAreUnique(t *testing.T) {
	t.Setenv("PROFILE", "/home/u/.zshrc")
	paths := completionCandidateRCPaths("linux", "/home/u")
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Errorf("duplicate candidate %q", p)
		}
		seen[p] = true
	}
	if !seen[filepath.Join("/home/u", ".config", "fish", "config.fish")] {
		t.Errorf("fish config missing from %v", paths)
	}
}

func TestInstallCompletionSkips(t *testing.T) {
	cases := []struct {
		name string
		opts installOptions
		tty  bool
	}{
		{"daemon only", installOptions{daemonOnly: true, autocompleteShell: "zsh"}, true},
		{"opted out", installOptions{noAutocomplete: true}, true},
		{"no terminal", installOptions{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := fakeHome(t)
			fakeInteractive(t, c.tty)
			fakeParent(t, "zsh")
			scriptConfirm(t, true)
			var out, errOut bytes.Buffer
			installCompletion(&out, &errOut, c.opts)
			if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
				t.Errorf("rc file was written: %v", err)
			}
			if out.Len() != 0 || errOut.Len() != 0 {
				t.Errorf("out = %q, errOut = %q, want silence", out.String(), errOut.String())
			}
		})
	}
}

func TestInstallCompletionExplicitShellDoesNotPrompt(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	asked := scriptConfirm(t)
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{autocompleteShell: "zsh"})

	if len(*asked) != 0 {
		t.Errorf("prompted despite an explicit shell: %v", *asked)
	}
	body, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("reading .zshrc: %v", err)
	}
	if strings.TrimSpace(string(body)) != completionLines["zsh"] {
		t.Errorf(".zshrc = %q", body)
	}
	if !strings.Contains(out.String(), "source ") {
		t.Errorf("no reload instruction: %q", out.String())
	}
}

func TestInstallCompletionPromptsWhenDetected(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	fakeParent(t, "fish")
	asked := scriptConfirm(t, true)
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{})

	want := filepath.Join(home, ".config", "fish", "config.fish")
	if len(*asked) != 1 || (*asked)[0] != want {
		t.Fatalf("prompted for %v, want [%s]", *asked, want)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("reading config.fish: %v", err)
	}
	if !strings.Contains(string(body), completionLines["fish"]) {
		t.Errorf("config.fish = %q", body)
	}
}

func TestInstallCompletionDeclinedWritesNothing(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	fakeParent(t, "zsh")
	scriptConfirm(t, false)
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{})

	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Errorf(".zshrc was written after a declined prompt: %v", err)
	}
}

func TestInstallCompletionIsIdempotent(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte(completionLines["zsh"]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{autocompleteShell: "zsh"})

	body, _ := os.ReadFile(rc)
	if strings.Count(string(body), completionMarker) != 1 {
		t.Errorf("line duplicated: %q", body)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("out = %q, errOut = %q, want silence", out.String(), errOut.String())
	}
}

func TestInstallCompletionUndetectableShellPrintsManualInstruction(t *testing.T) {
	fakeHome(t)
	fakeInteractive(t, true)
	fakeParent(t, "tmux")
	t.Setenv("SHELL", "/usr/bin/csh")
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{})

	if !strings.Contains(errOut.String(), "completion") {
		t.Errorf("errOut = %q, want a manual instruction", errOut.String())
	}
}

func TestInstallCompletionWriteFailureIsAdvisory(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	if err := os.Mkdir(filepath.Join(home, ".zshrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{autocompleteShell: "zsh"})

	if !strings.Contains(errOut.String(), ".zshrc") {
		t.Errorf("errOut = %q, want the rc path and the line to add by hand", errOut.String())
	}
	if !strings.Contains(errOut.String(), completionLines["zsh"]) {
		t.Errorf("errOut = %q, want the exact line", errOut.String())
	}
}

func TestInstallCompletionPromptFailureIsAdvisory(t *testing.T) {
	fakeHome(t)
	fakeInteractive(t, true)
	fakeParent(t, "zsh")
	failConfirm(t, errNoTerminal)
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{})

	if !strings.Contains(errOut.String(), completionLines["zsh"]) {
		t.Errorf("errOut = %q, want the manual instruction", errOut.String())
	}
}

func TestRunInstallCompletionFailureLeavesExitCodeZero(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	if err := os.Mkdir(filepath.Join(home, ".zshrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	err := runInstall(&out, installOptions{
		exe: exe, binDir: t.TempDir(), cliOnly: true,
		autocompleteShell: "zsh", errOut: &errOut,
	})
	if err != nil {
		t.Fatalf("runInstall = %v, want nil despite the completion failure", err)
	}
	if !strings.Contains(errOut.String(), ".zshrc") {
		t.Errorf("errOut = %q, want a warning", errOut.String())
	}
}

func TestRunInstallRejectsConflictingAutocompleteFlags(t *testing.T) {
	var out bytes.Buffer
	err := runInstall(&out, installOptions{
		cliOnly: true, autocompleteShell: "zsh", noAutocomplete: true,
	})
	if !errors.Is(err, errAutocompleteExclusive) {
		t.Errorf("runInstall err = %v, want errAutocompleteExclusive", err)
	}
}

func TestUninstallCompletionRemovesEveryRC(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	zshrc := filepath.Join(home, ".zshrc")
	bashrc := filepath.Join(home, ".bashrc")
	for _, p := range []string{zshrc, bashrc} {
		if err := os.WriteFile(p, []byte("export A=1\n"+completionLines["zsh"]+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	asked := scriptConfirm(t, true, false)
	var out, errOut bytes.Buffer
	uninstallCompletion(&out, &errOut, false)

	if len(*asked) != 2 {
		t.Fatalf("prompted for %v, want both rc files", *asked)
	}
	first, _ := os.ReadFile((*asked)[0])
	if strings.Contains(string(first), completionMarker) {
		t.Errorf("accepted file still has the line: %q", first)
	}
	second, _ := os.ReadFile((*asked)[1])
	if !strings.Contains(string(second), completionMarker) {
		t.Errorf("declined file was edited: %q", second)
	}
}

func TestUninstallCompletionSilentWhenNothingFound(t *testing.T) {
	fakeHome(t)
	fakeInteractive(t, true)
	scriptConfirm(t, true)
	var out, errOut bytes.Buffer
	uninstallCompletion(&out, &errOut, false)
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("out = %q, errOut = %q, want silence", out.String(), errOut.String())
	}
}

func TestUninstallCompletionSkipsUnderDaemonOnly(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte(completionLines["zsh"]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	uninstallCompletion(&out, &errOut, true)
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("out = %q, errOut = %q, want silence", out.String(), errOut.String())
	}
	body, _ := os.ReadFile(rc)
	if !strings.Contains(string(body), completionMarker) {
		t.Errorf(".zshrc was edited under --daemon-only: %q", body)
	}
}

func TestUninstallCompletionNonInteractiveListsFiles(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, false)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte(completionLines["zsh"]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	uninstallCompletion(&out, &errOut, false)

	if !strings.Contains(errOut.String(), rc) {
		t.Errorf("errOut = %q, want %s listed", errOut.String(), rc)
	}
	body, _ := os.ReadFile(rc)
	if !strings.Contains(string(body), completionMarker) {
		t.Errorf("rc was edited without a prompt: %q", body)
	}
}

func TestUninstallCompletionPromptFailureWarns(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(completionLines["zsh"]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	failConfirm(t, errNoTerminal)
	var out, errOut bytes.Buffer
	uninstallCompletion(&out, &errOut, false)
	if !strings.Contains(errOut.String(), ".zshrc") {
		t.Errorf("errOut = %q, want a warning", errOut.String())
	}
}

func TestUninstallCompletionUnreadableRCWarns(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	if err := os.Mkdir(filepath.Join(home, ".zshrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	uninstallCompletion(&out, &errOut, false)
	if !strings.Contains(errOut.String(), ".zshrc") {
		t.Errorf("errOut = %q, want a read warning", errOut.String())
	}
}

func TestCompletionHomeFailureIsAdvisory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	fakeInteractive(t, true)
	var out, errOut bytes.Buffer
	installCompletion(&out, &errOut, installOptions{autocompleteShell: "zsh"})
	if !strings.Contains(errOut.String(), "home directory") {
		t.Errorf("install errOut = %q", errOut.String())
	}
	errOut.Reset()
	uninstallCompletion(&out, &errOut, false)
	if !strings.Contains(errOut.String(), "home directory") {
		t.Errorf("uninstall errOut = %q", errOut.String())
	}
}

func TestConfirmCompletionAnswers(t *testing.T) {
	cases := []struct {
		keys []string
		want bool
	}{
		{[]string{"y"}, true},
		{[]string{"\r"}, true},
		{[]string{"n"}, false},
		{[]string{"\x03"}, false},
		{[]string{"?", "Y"}, true},
	}
	for _, c := range cases {
		scriptTerminal(t, c.keys...)
		got, err := confirmCompletion(io.Discard, "/home/u/.zshrc", completionLines["zsh"])
		if err != nil {
			t.Fatalf("confirmCompletion(%q): %v", c.keys, err)
		}
		if got != c.want {
			t.Errorf("confirmCompletion(%q) = %v, want %v", c.keys, got, c.want)
		}
	}

	scriptTerminal(t)
	if _, err := confirmCompletion(io.Discard, "/home/u/.zshrc", "line"); err == nil {
		t.Error("expected an error when the terminal yields no keys")
	}

	failTerminal(t, errNoTerminal)
	if _, err := confirmCompletion(io.Discard, "/home/u/.zshrc", "line"); !errors.Is(err, errNoTerminal) {
		t.Errorf("err = %v, want errNoTerminal", err)
	}
}

func TestParentProcessNameResolves(t *testing.T) {
	got := parentProcessName()
	if runtime.GOOS != "windows" && got == "" {
		t.Error("parentProcessName returned nothing for the test runner's parent")
	}
}

func TestCompletionShellValuesAreSorted(t *testing.T) {
	got := completionShellValues()
	want := []string{"bash", "fish", "powershell", "zsh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("completionShellValues() = %v, want %v", got, want)
	}
}

func TestCompletionColorFollowsNoColor(t *testing.T) {
	fakeInteractive(t, true)
	t.Setenv("NO_COLOR", "1")
	if completionColorEnabled() {
		t.Error("NO_COLOR did not disable colour")
	}
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	if !completionColorEnabled() {
		t.Error("colour should be enabled on an interactive terminal")
	}
	fakeInteractive(t, false)
	if completionColorEnabled() {
		t.Error("colour should be disabled off a terminal")
	}
}

func TestRunUninstallScansRCFilesForCompletion(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("export A=1\n"+completionLines["zsh"]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := scriptConfirm(t, true)

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, cliBinaryName), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := runUninstall(&out, uninstallOptions{binDir: binDir, cliOnly: true, errOut: &errOut}); err != nil {
		t.Fatalf("runUninstall: %v", err)
	}

	if len(*asked) != 1 || (*asked)[0] != rc {
		t.Fatalf("prompted for %v, want [%s]", *asked, rc)
	}
	body, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), completionMarker) {
		t.Errorf(".zshrc still sources completion: %q", body)
	}
	if !strings.Contains(string(body), "export A=1") {
		t.Errorf("unrelated rc lines were dropped: %q", body)
	}
	if !strings.Contains(out.String(), rc) {
		t.Errorf("out = %q, want the edited rc reported", out.String())
	}
}

func TestRunUninstallDaemonOnlyLeavesRCFilesAlone(t *testing.T) {
	home := fakeHome(t)
	fakeInteractive(t, true)
	rc := filepath.Join(home, ".zshrc")
	body := completionLines["zsh"] + "\n"
	if err := os.WriteFile(rc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := scriptConfirm(t, true)
	fakeServiceManager(t, &fakeLifecycle{})

	var out, errOut bytes.Buffer
	if err := runUninstall(&out, uninstallOptions{daemonOnly: true, errOut: &errOut}); err != nil {
		t.Fatalf("runUninstall --daemon-only: %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted under --daemon-only: %v", *asked)
	}
	got, _ := os.ReadFile(rc)
	if string(got) != body {
		t.Errorf(".zshrc = %q, want it untouched", got)
	}
}
