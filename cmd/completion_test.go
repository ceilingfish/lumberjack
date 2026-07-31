package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
)

func completionCmd(ctx context.Context) *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(ctx)
	return c
}

func TestCompleteRepositoryNames(t *testing.T) {
	serveService(t, &coverStub{repos: []*lumberjackv1.Repository{
		{DirPrefix: "alpha"}, {DirPrefix: "beta"},
	}})

	got := completeRepositoryNames(completionCmd(context.Background()))
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("completeRepositoryNames = %v, want the tracked repository names", got)
	}
}

func TestCompleteLogins(t *testing.T) {
	serveService(t, &coverStub{logins: []string{"personal", "work"}})

	got := completeLogins(completionCmd(context.Background()), "n")
	if len(got) != 2 || got[0] != "personal" {
		t.Errorf("completeLogins = %v, want the daemon's login list", got)
	}
}

func TestCompletionRPCFailureYieldsNoSuggestions(t *testing.T) {
	serveService(t, &coverStub{err: errors.New("boom")})

	if got := completeRepositoryNames(completionCmd(context.Background())); got != nil {
		t.Errorf("completeRepositoryNames = %v, want no suggestions", got)
	}
	if got := completeLogins(completionCmd(context.Background()), "n"); got != nil {
		t.Errorf("completeLogins = %v, want no suggestions", got)
	}
}

func TestCompletionDialFailureYieldsNoSuggestions(t *testing.T) {
	noDaemon(t)

	if got := completeRepositoryNames(completionCmd(context.Background())); got != nil {
		t.Errorf("completeRepositoryNames = %v, want no suggestions", got)
	}
	if got := completeLogins(completionCmd(context.Background()), "n"); got != nil {
		t.Errorf("completeLogins = %v, want no suggestions", got)
	}
}

func TestCompletionDeadlineYieldsNoSuggestions(t *testing.T) {
	serveService(t, &coverStub{
		block:  time.Minute,
		logins: []string{"work"},
		repos:  []*lumberjackv1.Repository{{DirPrefix: "alpha"}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if got := completeRepositoryNames(completionCmd(ctx)); got != nil {
		t.Errorf("completeRepositoryNames = %v, want no suggestions past the deadline", got)
	}
	if got := completeLogins(completionCmd(ctx), "n"); got != nil {
		t.Errorf("completeLogins = %v, want no suggestions past the deadline", got)
	}
}

func TestCompleteCommandWiring(t *testing.T) {
	serveService(t, &coverStub{
		repos:  []*lumberjackv1.Repository{{DirPrefix: "alpha"}},
		logins: []string{"work"},
	})

	cases := []struct {
		name   string
		args   []string
		want   string
		absent bool
	}{
		{name: "delete suggests repositories", args: []string{"delete", ""}, want: "alpha"},
		{name: "delete stops after the name", args: []string{"delete", "alpha", ""}, want: "alpha", absent: true},
		{name: "set-login suggests logins", args: []string{"set-login", ""}, want: "work"},
		{name: "set-login stops after the login", args: []string{"set-login", "work", ""}, want: "work", absent: true},
		{name: "--repository suggests repositories", args: []string{"status", "--repository", ""}, want: "alpha"},
		{name: "--lock-strategy lists its values", args: []string{"tidy", "--lock-strategy", ""}, want: "unlock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, "", append([]string{cobra.ShellCompRequestCmd}, c.args...)...)
			if err != nil {
				t.Fatalf("__complete %v: %v", c.args, err)
			}
			if got := strings.Contains(out, c.want); got == c.absent {
				t.Errorf("__complete %v = %q, contains %q = %v", c.args, out, c.want, got)
			}
		})
	}
}

func TestCompleteInitFiltersDirectories(t *testing.T) {
	out, err := run(t, "", cobra.ShellCompRequestCmd, "init", "")
	if err != nil {
		t.Fatalf("__complete init: %v", err)
	}
	if !strings.Contains(out, "ShellCompDirectiveFilterDirs") {
		t.Errorf("out = %q, want a directory-filter directive", out)
	}
	out, err = run(t, "", cobra.ShellCompRequestCmd, "init", ".", "")
	if err != nil {
		t.Fatalf("__complete init .: %v", err)
	}
	if !strings.Contains(out, "ShellCompDirectiveNoFileComp") {
		t.Errorf("out = %q, want no completion once a path is given", out)
	}
}

func TestCompleteSetupRemove(t *testing.T) {
	setupRepo(t)
	if _, err := run(t, "", "setup-steps", "add", "make setup"); err != nil {
		t.Fatalf("setup-steps add: %v", err)
	}
	out, err := run(t, "", cobra.ShellCompRequestCmd, "setup-steps", "remove", "")
	if err != nil {
		t.Fatalf("__complete setup-steps remove: %v", err)
	}
	if !strings.Contains(out, "make setup") {
		t.Errorf("out = %q, want the configured command suggested", out)
	}

	out, err = run(t, "", cobra.ShellCompRequestCmd, "setup-steps", "remove", "make setup", "")
	if err != nil {
		t.Fatalf("__complete setup-steps remove COMMAND: %v", err)
	}
	if strings.Contains(out, "make setup") {
		t.Errorf("out = %q, want no suggestion once COMMAND is given", out)
	}
}

func TestCompleteSetupRemoveOutsideARepository(t *testing.T) {
	t.Chdir(t.TempDir())
	out, err := run(t, "", cobra.ShellCompRequestCmd, "setup-steps", "remove", "")
	if err != nil {
		t.Fatalf("__complete setup-steps remove: %v", err)
	}
	if !strings.Contains(out, "ShellCompDirectiveNoFileComp") {
		t.Errorf("out = %q, want no suggestions with no config to read", out)
	}
}
