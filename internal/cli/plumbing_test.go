package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

// cmdWith returns a command wired to the given input and output, as Cobra
// would hand one to a RunE.
func cmdWith(in string, out io.Writer) *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(out)
	c.SetErr(io.Discard)
	c.SetIn(strings.NewReader(in))
	c.SetContext(context.Background())
	return c
}

func TestWithClientRunsWithAConnectedClient(t *testing.T) {
	serve(t, &stub{repos: []*lumberjackv1.Repository{{DirPrefix: "n"}}})

	var got int
	err := WithClient(cmdWith("", io.Discard), func(ctx context.Context, c *client.Client) error {
		repos, err := c.ListRepositories(ctx)
		got = len(repos)
		return err
	})
	if err != nil {
		t.Fatalf("WithClient: %v", err)
	}
	if got != 1 {
		t.Errorf("saw %d repositories, want 1", got)
	}
}

func TestWithClientSurfacesADialFailure(t *testing.T) {
	noDaemon(t)
	err := WithClient(cmdWith("", io.Discard), func(context.Context, *client.Client) error {
		t.Error("fn ran despite the dial failure")
		return nil
	})
	if err == nil {
		t.Error("expected the dial failure to surface")
	}
}

func TestCwdAbsResolvesTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got, err := CwdAbs()
	if err != nil {
		t.Fatalf("CwdAbs: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("CwdAbs = %q, want %q", resolved, want)
	}
}

func TestResolveRepositoryRefPrefersTheFlag(t *testing.T) {
	got, err := ResolveRepositoryRef("explicit")
	if err != nil {
		t.Fatalf("ResolveRepositoryRef: %v", err)
	}
	if got != "explicit" {
		t.Errorf("ref = %q, want the flag value", got)
	}
}

func TestResolveRepositoryRefFallsBackToTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got, err := ResolveRepositoryRef("")
	if err != nil {
		t.Fatalf("ResolveRepositoryRef: %v", err)
	}
	if got == "" || !filepath.IsAbs(got) {
		t.Errorf("ref = %q, want the absolute working directory", got)
	}
}

func TestAddRepositoryFlagRegistersTheFlagAndItsCompletion(t *testing.T) {
	serve(t, &stub{repos: []*lumberjackv1.Repository{{DirPrefix: "alpha"}, {DirPrefix: "beta"}}})

	var target string
	c := cmdWith("", io.Discard)
	AddRepositoryFlag(c, &target)

	flag := c.Flags().Lookup(RepositoryFlagName)
	if flag == nil {
		t.Fatalf("--%s was not registered", RepositoryFlagName)
	}
	if err := c.Flags().Set(RepositoryFlagName, "alpha"); err != nil {
		t.Fatal(err)
	}
	if target != "alpha" {
		t.Errorf("target = %q, want the flag bound", target)
	}

	fn := c.GetFlagCompletionFunc
	_ = fn
	got, directive := completionFor(t, c, RepositoryFlagName)
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(got) != 2 || got[0] != "alpha" {
		t.Errorf("suggestions = %v, want the tracked repository names", got)
	}
}

func completionFor(t *testing.T, c *cobra.Command, flag string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	fn, ok := c.GetFlagCompletionFunc(flag)
	if !ok {
		t.Fatalf("no completion registered for --%s", flag)
	}
	return fn(c, nil, "")
}

func TestCompleteRepositoryNames(t *testing.T) {
	serve(t, &stub{repos: []*lumberjackv1.Repository{{DirPrefix: "alpha"}}})
	if got := CompleteRepositoryNames(cmdWith("", io.Discard)); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("suggestions = %v, want [alpha]", got)
	}
}

func TestCompleteLogins(t *testing.T) {
	serve(t, &stub{logins: []string{"me", "work"}})
	if got := CompleteLogins(cmdWith("", io.Discard), "n"); len(got) != 2 {
		t.Errorf("suggestions = %v, want both logins", got)
	}
}

// Completion runs on every <TAB>: a down daemon or a failing RPC must yield no
// suggestions rather than an error or a stall.
func TestCompletionFailuresYieldNoSuggestions(t *testing.T) {
	noDaemon(t)
	if got := CompleteRepositoryNames(cmdWith("", io.Discard)); got != nil {
		t.Errorf("suggestions = %v, want none with no daemon", got)
	}

	serve(t, &stub{err: errors.New("boom")})
	if got := CompleteRepositoryNames(cmdWith("", io.Discard)); got != nil {
		t.Errorf("suggestions = %v, want none on an RPC failure", got)
	}
	if got := CompleteLogins(cmdWith("", io.Discard), "n"); got != nil {
		t.Errorf("suggestions = %v, want none on an RPC failure", got)
	}
}

func TestConfirmReadsTheAnswer(t *testing.T) {
	cases := map[string]bool{"y\n": true, "Y\n": true, "yes\n": true, "n\n": false, "\n": false, "": false}
	for in, want := range cases {
		var out bytes.Buffer
		if got := Confirm(cmdWith(in, &out), "Proceed?"); got != want {
			t.Errorf("Confirm(%q) = %v, want %v", in, got, want)
		}
		if !strings.Contains(out.String(), "Proceed? [y/N]") {
			t.Errorf("prompt = %q, want the question and the default", out.String())
		}
	}
}

func TestConfirmOnWritesToTheGivenWriter(t *testing.T) {
	var elsewhere bytes.Buffer
	c := cmdWith("y\n", io.Discard)
	if !ConfirmOn(c, &elsewhere, "Run?") {
		t.Error("ConfirmOn = false, want the y accepted")
	}
	if !strings.Contains(elsewhere.String(), "Run?") {
		t.Errorf("prompt went to %q, want the explicit writer", elsewhere.String())
	}
}

func TestOutputFormatResolvesTheFlag(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	c := cmdWith("", io.Discard)
	c.Flags().String(FormatFlagName, "", "")

	got, err := OutputFormat(c)
	if err != nil {
		t.Fatalf("OutputFormat: %v", err)
	}
	if got != present.Structured {
		t.Errorf("format = %v, want structured off a terminal", got)
	}

	if err := c.Flags().Set(FormatFlagName, "json"); err != nil {
		t.Fatal(err)
	}
	if got, err = OutputFormat(c); err != nil || got != present.JSON {
		t.Errorf("format = %v (err %v), want json", got, err)
	}

	if err := c.Flags().Set(FormatFlagName, "yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := OutputFormat(c); err == nil {
		t.Error("expected an unknown --format to error")
	}
}

func TestOutputFormatWithoutTheFlagRegistered(t *testing.T) {
	if _, err := OutputFormat(cmdWith("", io.Discard)); err == nil {
		t.Error("expected a missing --format flag to error")
	}
}

func TestLoadWorktreeConfig(t *testing.T) {
	dir := gitRepo(t)
	body := "steps:\n  - type: run-command\n    run_command:\n      command: make setup\n"
	if err := os.WriteFile(filepath.Join(dir, setup.ConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root, cfg, err := LoadWorktreeConfig()
	if err != nil {
		t.Fatalf("LoadWorktreeConfig: %v", err)
	}
	if root == "" || cfg == nil {
		t.Fatalf("root = %q, cfg = %v, want both populated", root, cfg)
	}
	if len(cfg.RunCommands()) != 1 {
		t.Errorf("commands = %v, want the one configured step", cfg.RunCommands())
	}
}

func TestLoadWorktreeConfigSurfacesAResolveFailure(t *testing.T) {
	dir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, setup.ConfigFileName), []byte("steps: [[[\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := LoadWorktreeConfig(); err == nil {
		t.Error("expected the malformed config to surface")
	}
}

func TestSetupConsentPending(t *testing.T) {
	cases := []struct {
		name string
		repo *lumberjackv1.Repository
		want bool
	}{
		{"no steps", &lumberjackv1.Repository{}, false},
		{"steps trusted", &lumberjackv1.Repository{SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make"}, IsTrusted: true}}, false},
		{"steps pending", &lumberjackv1.Repository{SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make"}}}, true},
	}
	for _, c := range cases {
		if got := SetupConsentPending(c.repo); got != c.want {
			t.Errorf("%s: SetupConsentPending = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPromptSetupConsentRecordsAYes(t *testing.T) {
	serve(t, &stub{})
	var out bytes.Buffer
	repo := &lumberjackv1.Repository{
		DirPrefix:  "n",
		SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make setup"}, CurrentChecksum: "c"},
	}
	if err := PromptSetupConsent(context.Background(), cmdWith("y\n", &out), dial(t), "n", repo); err != nil {
		t.Fatalf("PromptSetupConsent: %v", err)
	}
	for _, want := range []string{"make setup", "Consent recorded."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}
}

func TestPromptSetupConsentDeclined(t *testing.T) {
	serve(t, &stub{})
	var out bytes.Buffer
	repo := &lumberjackv1.Repository{
		DirPrefix:  "n",
		SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make setup"}},
	}
	if err := PromptSetupConsent(context.Background(), cmdWith("n\n", &out), dial(t), "n", repo); err != nil {
		t.Fatalf("PromptSetupConsent: %v", err)
	}
	if !strings.Contains(out.String(), "Not consented") {
		t.Errorf("out = %q, want the declined notice", out.String())
	}
}

// A .lumberjack.yml that changed while the user was reading it is reported
// rather than silently recorded against the stale checksum.
func TestPromptSetupConsentReportsARejectedChecksum(t *testing.T) {
	serve(t, &stub{sendErr: true})
	var out bytes.Buffer
	repo := &lumberjackv1.Repository{
		DirPrefix:  "n",
		SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make setup"}},
	}
	if err := PromptSetupConsent(context.Background(), cmdWith("y\n", &out), dial(t), "n", repo); err != nil {
		t.Fatalf("PromptSetupConsent: %v", err)
	}
	if !strings.Contains(out.String(), "changed while you were reading it") {
		t.Errorf("out = %q, want the stale-checksum notice", out.String())
	}
}

func TestPromptSetupConsentWithNothingPendingDoesNothing(t *testing.T) {
	var out bytes.Buffer
	repo := &lumberjackv1.Repository{DirPrefix: "n"}
	if err := PromptSetupConsent(context.Background(), cmdWith("", &out), nil, "n", repo); err != nil {
		t.Fatalf("PromptSetupConsent: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("out = %q, want silence when no consent is pending", out.String())
	}
}

func TestPromptSetupConsentSurfacesAFailedRecord(t *testing.T) {
	serve(t, &stub{consentErr: errors.New("boom")})
	repo := &lumberjackv1.Repository{
		DirPrefix:  "n",
		SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make setup"}},
	}
	err := PromptSetupConsent(context.Background(), cmdWith("y\n", io.Discard), dial(t), "n", repo)
	if err == nil {
		t.Error("expected the failed consent record to surface")
	}
}

func TestTerminalHelpersReportNoTerminalUnderGoTest(t *testing.T) {
	if InteractiveTerminal() {
		t.Error("InteractiveTerminal = true, want false under `go test`")
	}
	if _, _, err := RawTerminal(); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("RawTerminal err = %v, want ErrNoTerminal", err)
	}
}

func TestPromptSetupConsentSurfacesFailedWrites(t *testing.T) {
	cases := map[string]int{"the preamble": 0, "the command list": 1}
	for name, succeed := range cases {
		t.Run(name, func(t *testing.T) {
			serve(t, &stub{})
			repo := &lumberjackv1.Repository{
				DirPrefix:  "n",
				SetupSteps: &lumberjackv1.SetupSteps{Steps: []string{"make setup"}},
			}
			cmd := cmdWith("y\n", &flakyWriter{succeed: succeed})
			err := PromptSetupConsent(context.Background(), cmd, dial(t), "n", repo)
			if !errors.Is(err, errWrite) {
				t.Errorf("err = %v, want the failed write", err)
			}
		})
	}
}
