package cmd

import (
	"errors"
	"io"
	"strings"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func onATerminal(t *testing.T) {
	t.Helper()
	prev := interactiveTerminal
	interactiveTerminal = func() bool { return true }
	t.Cleanup(func() { interactiveTerminal = prev })
}

func TestPromptLockStrategy(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		want lumberjackv1.LockStrategy
	}{
		{name: "enter unlocks", keys: []string{enterKey}, want: lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK},
		{name: "s skips", keys: []string{"s"}, want: lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP},
		{name: "d deletes the lock", keys: []string{"d"}, want: lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE},
		{name: "n aborts", keys: []string{"n"}, want: lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT},
		{name: "an unrecognised key keeps waiting", keys: []string{"z", "s"}, want: lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			onATerminal(t)
			scriptTerminal(t, c.keys...)
			cmd, question := pickerCmd(t)

			got, err := promptLockStrategy(cmd, "/elsewhere/foo", "in use")
			if err != nil {
				t.Fatalf("promptLockStrategy: %v", err)
			}
			if got != c.want {
				t.Errorf("promptLockStrategy = %v, want %v", got, c.want)
			}
			if !strings.Contains(question.String(), "/elsewhere/foo (in use)") {
				t.Errorf("question = %q, want the worktree and its lock reason", question.String())
			}
		})
	}
}

func TestPromptLockStrategyWithoutALockReason(t *testing.T) {
	onATerminal(t)
	scriptTerminal(t, enterKey)
	cmd, question := pickerCmd(t)

	if _, err := promptLockStrategy(cmd, "/elsewhere/foo", ""); err != nil {
		t.Fatalf("promptLockStrategy: %v", err)
	}
	if strings.Contains(question.String(), "(") {
		t.Errorf("question = %q, want no empty parenthesised reason", question.String())
	}
}

func TestPromptLockStrategyInputEndsWithoutAnAnswer(t *testing.T) {
	onATerminal(t)
	scriptTerminal(t)
	cmd, _ := pickerCmd(t)

	if _, err := promptLockStrategy(cmd, "/elsewhere/foo", ""); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestPromptLockStrategyWithoutATerminal(t *testing.T) {
	prev := interactiveTerminal
	interactiveTerminal = func() bool { return false }
	t.Cleanup(func() { interactiveTerminal = prev })
	cmd, _ := pickerCmd(t)

	_, err := promptLockStrategy(cmd, "/elsewhere/foo", "")
	if err == nil || !strings.Contains(err.Error(), "--lock-strategy") {
		t.Errorf("err = %v, want advice to pass --lock-strategy", err)
	}
}

func TestPromptLockStrategyRawModeFailure(t *testing.T) {
	onATerminal(t)
	boom := errors.New("entering raw terminal mode: boom")
	failTerminal(t, boom)
	cmd, _ := pickerCmd(t)

	if _, err := promptLockStrategy(cmd, "/elsewhere/foo", ""); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the raw-mode failure", err)
	}
}

func TestCmdTidyPromptsThroughTheRealPrompter(t *testing.T) {
	stub := &coverStub{tidyMoves: []*lumberjackv1.TidyMove{{
		Repository: "n", Branch: "feature/foo", From: "/elsewhere/foo", To: "/p/n-foo",
		Locked: true, LockReason: "in use",
	}}}
	serveService(t, stub)
	onATerminal(t)
	scriptTerminal(t, "s")

	if _, err := run(t, "", "tidy", "--repository", "n"); err != nil {
		t.Fatalf("tidy: %v", err)
	}
}

func TestLockStrategyValuesAreSorted(t *testing.T) {
	got := lockStrategyValues()
	want := []string{"abort", "delete", "skip", "unlock"}
	if len(got) != len(want) {
		t.Fatalf("lockStrategyValues = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lockStrategyValues = %v, want %v", got, want)
		}
	}
}
