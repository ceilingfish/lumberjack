package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// lockStrategyNames maps the --lock-strategy values to the proto enum. They are
// the same four choices the interactive prompt offers, so a user who answers a
// prompt can pin that answer with the flag next time.
var lockStrategyNames = map[string]lumberjackv1.LockStrategy{
	"skip":   lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP,
	"unlock": lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK,
	"delete": lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE,
	"abort":  lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT,
}

// errLockAbort is the outcome of choosing to abort, at the prompt or via
// --lock-strategy abort: the command stops without moving anything.
var errLockAbort = errors.New("aborted: the worktree is locked")

// lockStrategyValues lists the accepted --lock-strategy values, for the flag's
// error message and its shell completion.
func lockStrategyValues() []string {
	names := make([]string, 0, len(lockStrategyNames))
	for name := range lockStrategyNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parseLockStrategy resolves a --lock-strategy value. An empty value leaves the
// strategy unspecified, which is how the caller knows to prompt instead.
func parseLockStrategy(value string) (lumberjackv1.LockStrategy, error) {
	if value == "" {
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED, nil
	}
	s, ok := lockStrategyNames[value]
	if !ok {
		return 0, fmt.Errorf("invalid --lock-strategy %q: want one of %v", value, lockStrategyValues())
	}
	return s, nil
}

// lockPrompter asks what to do about one locked worktree. It is a package var so
// tests can substitute a scripted answer for the raw-terminal UI.
var lockPrompter = promptLockStrategy

// interactiveStdin reports whether there is a terminal to prompt on. A package
// var for the same reason as lockPrompter.
var interactiveStdin = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// promptLockStrategy asks, on the controlling terminal, what to do about the
// locked worktree at path (reason being the message git recorded with the lock,
// if any). Enter takes the default: unlock for the move and lock it again
// afterwards, which leaves the worktree as the user left it.
func promptLockStrategy(cmd *cobra.Command, path, reason string) (lumberjackv1.LockStrategy, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return 0, errors.New("no interactive terminal to ask about a locked worktree; pass --lock-strategy")
	}
	out := cmd.ErrOrStderr()

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 0, fmt.Errorf("entering raw terminal mode: %w", err)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	locked := path
	if reason != "" {
		locked += fmt.Sprintf(" (%s)", reason)
	}
	_, _ = fmt.Fprintf(out,
		"The worktree at %s is locked, do you want to temporarily unlock the worktree to allow it to be moved?\r\n"+
			"  [Y] yes, unlock it for the move and lock it again  [s] skip this worktree  "+
			"[d] delete the lock  [n] abort\r\n", locked)

	for {
		s, err := readLockAnswer(os.Stdin)
		if err != nil {
			return 0, err
		}
		if s == lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED {
			continue // an unrecognised key: keep waiting rather than guessing
		}
		return s, nil
	}
}

// readLockAnswer reads one keypress and maps it to a strategy, returning
// UNSPECIFIED for a key that means nothing here. Enter takes the default
// (unlock); Ctrl-C aborts, as it would anywhere else.
func readLockAnswer(r io.Reader) (lumberjackv1.LockStrategy, error) {
	buf := make([]byte, 3)
	n, err := r.Read(buf)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED, nil
	}
	switch buf[0] {
	case 'y', 'Y', '\r', '\n':
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK, nil
	case 's', 'S':
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP, nil
	case 'd', 'D':
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE, nil
	case 'n', 'N', 0x03:
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT, nil
	default:
		return lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED, nil
	}
}
