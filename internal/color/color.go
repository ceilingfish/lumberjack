// Package color adds optional ANSI colourisation to command output. It is a
// small, reusable unit deliberately kept separate from any one command's
// render logic: the `--format` formatter (a later addition) reuses the same
// palette and enable/disable decision via its own "color" mode.
//
// Colour is opt-out, not opt-in: it is emitted whenever the destination is a
// real terminal and the user hasn't set NO_COLOR, and it is silently dropped
// otherwise. Callers never need to branch on Enabled() themselves — the
// category functions below (Action, Path, Branch, ...) already return their
// argument unchanged when colour is disabled, so output is byte-for-byte the
// same as if colour didn't exist.
package color

import (
	"os"

	"golang.org/x/term"
)

// Standard 8/16-colour ANSI SGR codes (no truecolour), so output renders
// correctly on any terminal and respects the user's light/dark theme. Ordinary
// text is left alone (terminal default foreground) — never hard-coded
// white/black.
const (
	sgrAction  = "36" // cyan: verbs/operations (checked out / adopted / deleted)
	sgrPath    = "34" // blue: file-system paths
	sgrBranch  = "32" // green: git branch names
	sgrOK      = "32" // green: status ok
	sgrWarning = "33" // yellow: status warning (⚠)
	sgrError   = "31" // red: status error
	sgrDim     = "2"  // dim: de-emphasis (e.g. "-" / "never")

	sgrReset = "\x1b[0m"
)

// Decide is the pure enable/disable rule Enabled applies: colour requires
// both a real terminal destination and NO_COLOR being unset. Per
// no-color.org, NO_COLOR disables colour by its mere presence, regardless of
// its value.
func Decide(isTerminal, noColorSet bool) bool {
	return isTerminal && !noColorSet
}

// Enabled reports whether colour output should currently be emitted. It is a
// var (not a plain func) so tests — including in other packages — can
// substitute a fixed decision instead of needing a real controlling terminal
// or mutating the process environment.
//
// The terminal check uses the real stdout file descriptor (the same
// mechanism the interactive login picker uses for stdin), because the render
// layer only ever sees the abstract io.Writer Cobra hands it, which in tests
// is a buffer rather than the process's actual stdout.
var Enabled = func() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return Decide(term.IsTerminal(int(os.Stdout.Fd())), noColor)
}

// paint wraps s in the given SGR code, or returns it unchanged when colour is
// disabled or s is empty (nothing to colourise).
func paint(sgr, s string) string {
	if s == "" || !Enabled() {
		return s
	}
	return "\x1b[" + sgr + "m" + s + sgrReset
}

// Action colourises a verb/operation (e.g. "checked out", "deleted").
func Action(s string) string { return paint(sgrAction, s) }

// Path colourises a file-system path.
func Path(s string) string { return paint(sgrPath, s) }

// Branch colourises a git branch name.
func Branch(s string) string { return paint(sgrBranch, s) }

// OK colourises an "ok" status.
func OK(s string) string { return paint(sgrOK, s) }

// Warning colourises a warning status (e.g. "⚠ ...").
func Warning(s string) string { return paint(sgrWarning, s) }

// Error colourises an error status.
func Error(s string) string { return paint(sgrError, s) }

// Dim de-emphasises placeholder text such as "-" or "never".
func Dim(s string) string { return paint(sgrDim, s) }
