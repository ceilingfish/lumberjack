package cmd

import (
	"strings"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func TestReadLockAnswerClassifiesKeys(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want lumberjackv1.LockStrategy
	}{
		{"y unlocks", "y", lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK},
		{"upper Y unlocks", "Y", lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK},
		{"enter takes the default", "\r", lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK},
		{"newline takes the default", "\n", lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK},
		{"s skips", "s", lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP},
		{"d deletes the lock", "d", lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE},
		{"n aborts", "n", lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT},
		{"ctrl-c aborts", "\x03", lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT},
		// An unrecognised key is not an answer: the prompt keeps waiting rather
		// than guessing at something as destructive as deleting a lock.
		{"other key means nothing", "x", lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readLockAnswer(strings.NewReader(c.in))
			if err != nil {
				t.Fatalf("readLockAnswer(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("readLockAnswer(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestParseLockStrategy(t *testing.T) {
	cases := map[string]lumberjackv1.LockStrategy{
		"":       lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED, // no flag: ask
		"skip":   lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP,
		"unlock": lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK,
		"delete": lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE,
		"abort":  lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT,
	}
	for value, want := range cases {
		got, err := parseLockStrategy(value)
		if err != nil {
			t.Fatalf("parseLockStrategy(%q): %v", value, err)
		}
		if got != want {
			t.Errorf("parseLockStrategy(%q) = %v, want %v", value, got, want)
		}
	}
	if _, err := parseLockStrategy("unlck"); err == nil {
		t.Error("parseLockStrategy accepted a misspelt value")
	}
}
