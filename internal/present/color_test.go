package present

import "testing"

var colorHelpers = []struct {
	name string
	fn   func(string, bool) string
	code string
}{
	{"Action", Action, "36"},
	{"Path", Path, "34"},
	{"Branch", Branch, "32"},
	{"StatusOK", StatusOK, "32"},
	{"StatusWarn", StatusWarn, "33"},
	{"StatusErr", StatusErr, "31"},
	{"Dim", Dim, "02"},
	{"Neutral", Neutral, "39"},
}

func TestColorHelpersEmitExactBytesWhenEnabled(t *testing.T) {
	for _, h := range colorHelpers {
		t.Run(h.name, func(t *testing.T) {
			got := h.fn("value", true)
			want := "\x1b[" + h.code + "mvalue\x1b[0m"
			if got != want {
				t.Errorf("%s(%q, true) = %q, want %q", h.name, "value", got, want)
			}
		})
	}
}

func TestColorHelpersPassThroughWhenDisabled(t *testing.T) {
	inputs := []string{"", "value", "-", "never", "\x1b[31malready\x1b[0m"}
	for _, h := range colorHelpers {
		t.Run(h.name, func(t *testing.T) {
			for _, in := range inputs {
				if got := h.fn(in, false); got != in {
					t.Errorf("%s(%q, false) = %q, want it unchanged with no escape bytes added", h.name, in, got)
				}
			}
		})
	}
}

func TestColorHelperOverheadIsConstantLength(t *testing.T) {
	const payload = "cell"
	want := len("\x1b[00m") + len("\x1b[0m")
	for _, h := range colorHelpers {
		if got := len(h.fn(payload, true)) - len(payload); got != want {
			t.Errorf("%s overhead = %d bytes, want %d (a 2-digit SGR opener plus reset; table columns misalign otherwise)", h.name, got, want)
		}
	}
}

func TestDimKeepsLeadingZero(t *testing.T) {
	if got, want := Dim("-", true), "\x1b[02m-\x1b[0m"; got != want {
		t.Errorf("Dim = %q, want %q: the zero-padded code, not the 1-digit \\x1b[2m form", got, want)
	}
}

func TestColorizeWrapsPayloadVerbatim(t *testing.T) {
	if got, want := colorize("36", "", true), "\x1b[36m\x1b[0m"; got != want {
		t.Errorf("colorize empty = %q, want %q", got, want)
	}
	if got, want := colorize("36", "a\tb\n", true), "\x1b[36ma\tb\n\x1b[0m"; got != want {
		t.Errorf("colorize = %q, want %q", got, want)
	}
}
