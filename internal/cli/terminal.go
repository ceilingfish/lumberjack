package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

var ErrNoTerminal = errors.New("no interactive terminal")

// InteractiveTerminal reports whether there is a terminal to prompt on. Both
// ends have to be one: stdin because the answer is read from it, and stderr
// because the question is written there — with stderr redirected,
// `lumberjack tidy 2>/dev/null` would otherwise sit in raw mode waiting for an
// answer to a question the user never saw. A package var so tests can
// substitute a scripted answer for the raw-terminal UI.
var InteractiveTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// RawTerminal puts stdin into raw mode and returns it alongside a restore
// func. A package var so tests can substitute a scripted reader.
var RawTerminal = func() (io.Reader, func(), error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, nil, ErrNoTerminal
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, nil, fmt.Errorf("entering raw terminal mode: %w", err)
	}
	return os.Stdin, func() { _ = term.Restore(fd, oldState) }, nil
}
