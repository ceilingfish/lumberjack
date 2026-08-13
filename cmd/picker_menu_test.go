package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type keyReader struct{ keys []string }

func (k *keyReader) Read(p []byte) (int, error) {
	if len(k.keys) == 0 {
		return 0, io.EOF
	}
	n := copy(p, k.keys[0])
	k.keys = k.keys[1:]
	return n, nil
}

func scriptTerminal(t *testing.T, keys ...string) {
	t.Helper()
	prev := rawTerminal
	rawTerminal = func() (io.Reader, func(), error) {
		return &keyReader{keys: keys}, func() {}, nil
	}
	t.Cleanup(func() { rawTerminal = prev })
}

func failTerminal(t *testing.T, err error) {
	t.Helper()
	prev := rawTerminal
	rawTerminal = func() (io.Reader, func(), error) { return nil, nil, err }
	t.Cleanup(func() { rawTerminal = prev })
}

const (
	upKey    = "\x1b[A"
	downKey  = "\x1b[B"
	enterKey = "\r"
)

func pickerCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var menu bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(io.Discard)
	c.SetErr(&menu)
	return c, &menu
}

func TestPickLogin(t *testing.T) {
	logins := []string{"personal", "work", "client"}
	cases := []struct {
		name    string
		keys    []string
		current string
		want    string
	}{
		{name: "enterKey takes the preselected login", keys: []string{enterKey}, current: "work", want: "work"},
		{name: "absent current starts at the first login", keys: []string{enterKey}, current: "ghost", want: "personal"},
		{name: "downKey moves to the next login", keys: []string{downKey, enterKey}, want: "work"},
		{name: "downKey stops at the last login", keys: []string{downKey, downKey, downKey, enterKey}, want: "client"},
		{name: "upKey moves back", keys: []string{downKey, downKey, upKey, enterKey}, want: "work"},
		{name: "upKey stops at the first login", keys: []string{upKey, upKey, enterKey}, want: "personal"},
		{name: "an unrecognised key is ignored", keys: []string{"x", downKey, enterKey}, want: "work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scriptTerminal(t, c.keys...)
			cmd, menu := pickerCmd(t)

			got, err := pickLogin(cmd, logins, c.current)
			if err != nil {
				t.Fatalf("pickLogin: %v", err)
			}
			if got != c.want {
				t.Errorf("pickLogin = %q, want %q", got, c.want)
			}
			if !strings.Contains(menu.String(), "Select a gh account") {
				t.Errorf("menu = %q, want the prompt header", menu.String())
			}
			for _, l := range logins {
				if !strings.Contains(menu.String(), l) {
					t.Errorf("menu = %q, missing login %q", menu.String(), l)
				}
			}
		})
	}
}

func TestPickLoginHighlightsTheSelection(t *testing.T) {
	scriptTerminal(t, enterKey)
	cmd, menu := pickerCmd(t)

	if _, err := pickLogin(cmd, []string{"personal", "work"}, "work"); err != nil {
		t.Fatalf("pickLogin: %v", err)
	}
	if !strings.Contains(menu.String(), "\x1b[7m> work") {
		t.Errorf("menu = %q, want the current login highlighted", menu.String())
	}
	if !strings.Contains(menu.String(), "  personal") {
		t.Errorf("menu = %q, want the unselected login unhighlighted", menu.String())
	}
}

func TestPickLoginCancelled(t *testing.T) {
	scriptTerminal(t, "q")
	cmd, _ := pickerCmd(t)

	if _, err := pickLogin(cmd, []string{"personal"}, ""); !errors.Is(err, errPickCancelled) {
		t.Errorf("err = %v, want errPickCancelled", err)
	}
}

func TestPickLoginInputEndsWithoutAnAnswer(t *testing.T) {
	scriptTerminal(t)
	cmd, menu := pickerCmd(t)

	_, err := pickLogin(cmd, []string{"personal"}, "")
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
	if !strings.Contains(menu.String(), "\x1b[J") {
		t.Errorf("menu = %q, want the menu cleared before returning", menu.String())
	}
}

func TestPickLoginWithoutATerminal(t *testing.T) {
	failTerminal(t, errNoTerminal)
	cmd, _ := pickerCmd(t)

	_, err := pickLogin(cmd, []string{"personal"}, "")
	if err == nil || !strings.Contains(err.Error(), "pass a login") {
		t.Errorf("err = %v, want advice to pass a login explicitly", err)
	}
}

func TestPickLoginRawModeFailure(t *testing.T) {
	boom := errors.New("entering raw terminal mode: boom")
	failTerminal(t, boom)
	cmd, _ := pickerCmd(t)

	if _, err := pickLogin(cmd, []string{"personal"}, ""); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the raw-mode failure", err)
	}
}

func TestCmdSetLoginPickerCancelled(t *testing.T) {
	serveService(t, &coverStub{logins: []string{"personal"}})
	scriptTerminal(t, "q")

	if _, err := run(t, "", "set-login", "--repository", "n"); !errors.Is(err, errPickCancelled) {
		t.Errorf("err = %v, want the cancelled pick to abort set-login", err)
	}
}
