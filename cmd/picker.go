package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// errPickCancelled is returned by the picker when the user aborts the menu.
var errPickCancelled = errors.New("cancelled")

// loginPicker chooses a login from candidates interactively. It is a package
// var so tests can substitute a deterministic selection for the raw-terminal
// UI.
var loginPicker = pickLogin

// keyAction is what a keypress maps to in the menu loop.
type keyAction int

const (
	keyNone keyAction = iota // ignored keypress — repaint nothing
	keyUp
	keyDown
	keyConfirm
	keyCancel
)

// pickLogin shows an arrow-key menu of logins on the controlling terminal and
// returns the chosen one. If current is one of logins it starts selected.
//
// It requires an interactive terminal: with no login to preselect and no TTY to
// read from, there is nothing to choose with, so it errors and tells the caller
// to pass a login explicitly.
func pickLogin(cmd *cobra.Command, logins []string, current string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no login given and no interactive terminal to choose one; pass a login, e.g. `lumberjack set-login LOGIN`")
	}
	out := cmd.ErrOrStderr()
	sel := indexOf(logins, current)

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("entering raw terminal mode: %w", err)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	_, _ = fmt.Fprint(out, "Select a gh account (↑/↓ to move, enter to confirm, q to cancel):\r\n")
	renderMenu(out, logins, sel)

	for {
		action, err := readAction(os.Stdin)
		if err != nil {
			clearMenu(out, len(logins))
			return "", err
		}
		switch action {
		case keyUp:
			if sel > 0 {
				sel--
			}
		case keyDown:
			if sel < len(logins)-1 {
				sel++
			}
		case keyConfirm:
			clearMenu(out, len(logins))
			return logins[sel], nil
		case keyCancel:
			clearMenu(out, len(logins))
			return "", errPickCancelled
		case keyNone:
			continue // ignore without repainting
		}
		// Jump back to the top of the list and redraw with the new selection.
		_, _ = fmt.Fprintf(out, "\x1b[%dA", len(logins))
		renderMenu(out, logins, sel)
	}
}

// readAction reads one keypress and classifies it. Arrow keys arrive as a
// three-byte escape sequence (ESC [ A/B); enter is CR/LF; q or Ctrl-C cancels.
func readAction(r io.Reader) (keyAction, error) {
	buf := make([]byte, 3)
	n, err := r.Read(buf)
	if err != nil {
		return keyNone, err
	}
	key := buf[:n]
	switch {
	case n == 3 && key[0] == 0x1b && key[1] == '[' && key[2] == 'A':
		return keyUp, nil
	case n == 3 && key[0] == 0x1b && key[1] == '[' && key[2] == 'B':
		return keyDown, nil
	case n >= 1 && (key[0] == '\r' || key[0] == '\n'):
		return keyConfirm, nil
	case n >= 1 && (key[0] == 'q' || key[0] == 0x03):
		return keyCancel, nil
	default:
		return keyNone, nil
	}
}

// indexOf returns the position of want in ss, or 0 if it is absent.
func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return 0
}

// renderMenu draws one line per login, highlighting the selected row in reverse
// video. Each line is cleared to its end (\x1b[K) so a longer previous label
// can't leave stray characters behind.
func renderMenu(out io.Writer, logins []string, sel int) {
	for i, l := range logins {
		if i == sel {
			_, _ = fmt.Fprintf(out, "\x1b[7m> %s\x1b[0m\x1b[K\r\n", l)
		} else {
			_, _ = fmt.Fprintf(out, "  %s\x1b[K\r\n", l)
		}
	}
}

// clearMenu removes the header and the n menu lines, leaving the cursor where
// the header began, so the selection result (or nothing) prints cleanly.
func clearMenu(out io.Writer, n int) {
	_, _ = fmt.Fprintf(out, "\x1b[%dA\r\x1b[J", n+1)
}
