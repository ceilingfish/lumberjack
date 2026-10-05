package worktree

import (
	"context"
	"slices"
	"testing"
)

func TestCommandCannotPromptOnTheTerminal(t *testing.T) {
	g := &Git{bin: "git"}
	cmd, _, cancel := g.command(context.Background(), "", "fetch")
	defer cancel()

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("git does not run in its own session, so ssh can read the passphrase from the controlling terminal")
	}
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}
