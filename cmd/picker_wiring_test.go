package cmd

import (
	"errors"
	"io"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/cli"
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
	prev := cli.RawTerminal
	cli.RawTerminal = func() (io.Reader, func(), error) {
		return &keyReader{keys: keys}, func() {}, nil
	}
	t.Cleanup(func() { cli.RawTerminal = prev })
}

func TestCmdSetLoginPickerCancelled(t *testing.T) {
	serveService(t, &coverStub{logins: []string{"personal"}})
	scriptTerminal(t, "q")

	if _, err := run(t, "", "set-login", "--repository", "n"); !errors.Is(err, cli.ErrPickCancelled) {
		t.Errorf("err = %v, want the cancelled pick to abort set-login", err)
	}
}
