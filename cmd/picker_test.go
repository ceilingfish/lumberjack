package cmd

import (
	"strings"
	"testing"
)

func TestReadActionClassifiesKeys(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want keyAction
	}{
		{"up arrow", "\x1b[A", keyUp},
		{"down arrow", "\x1b[B", keyDown},
		{"enter CR", "\r", keyConfirm},
		{"enter LF", "\n", keyConfirm},
		{"q cancels", "q", keyCancel},
		{"ctrl-c cancels", "\x03", keyCancel},
		{"other key ignored", "x", keyNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readAction(strings.NewReader(c.in))
			if err != nil {
				t.Fatalf("readAction(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("readAction(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIndexOf(t *testing.T) {
	ss := []string{"personal", "work"}
	if got := indexOf(ss, "work"); got != 1 {
		t.Errorf("indexOf work = %d, want 1", got)
	}
	// Absent value falls back to the first entry.
	if got := indexOf(ss, "ghost"); got != 0 {
		t.Errorf("indexOf absent = %d, want 0", got)
	}
}
