package present

import (
	"strings"
	"testing"
)

type colorHelper struct {
	name string
	fn   func(string, bool) string
	code string
}

func colorHelpers() []colorHelper {
	return []colorHelper{
		{"Action", Action, "36"},
		{"Path", Path, "34"},
		{"Branch", Branch, "32"},
		{"StatusOK", StatusOK, "32"},
		{"StatusWarn", StatusWarn, "33"},
		{"StatusErr", StatusErr, "31"},
		{"Dim", Dim, "02"},
		{"Neutral", Neutral, "39"},
	}
}

func TestColorHelpersEmitExactBytesWhenEnabled(t *testing.T) {
	for _, h := range colorHelpers() {
		t.Run(h.name, func(t *testing.T) {
			got := h.fn("value", true)
			want := "\x1b[" + h.code + "m" + "value" + "\x1b[0m"
			if got != want {
				t.Errorf("%s(%q, true) = %q, want %q", h.name, "value", got, want)
			}
		})
	}
}

func TestColorHelpersPassThroughWhenDisabled(t *testing.T) {
	inputs := []string{"", "value", "-", "never", "\x1b[31malready\x1b[0m"}
	for _, h := range colorHelpers() {
		t.Run(h.name, func(t *testing.T) {
			for _, in := range inputs {
				got := h.fn(in, false)
				if got != in {
					t.Errorf("%s(%q, false) = %q, want unchanged", h.name, in, got)
				}
			}
			if strings.ContainsRune(h.fn("value", false), 0x1b) {
				t.Errorf("%s emitted an escape byte while disabled", h.name)
			}
		})
	}
}

func TestColorHelperOverheadIsConstantLength(t *testing.T) {
	const payload = "cell"
	want := -1
	for _, h := range colorHelpers() {
		overhead := len(h.fn(payload, true)) - len(payload)
		if want == -1 {
			want = overhead
		}
		if overhead != want {
			t.Errorf("%s overhead = %d bytes, want %d (table columns misalign otherwise)", h.name, overhead, want)
		}
	}
	if want != len("\x1b[00m")+len("\x1b[0m") {
		t.Errorf("overhead = %d bytes, want a 2-digit SGR opener plus reset", want)
	}
}

func TestColorHelperSGRParametersAreTwoDigits(t *testing.T) {
	for _, h := range colorHelpers() {
		if len(h.code) != 2 {
			t.Errorf("%s uses SGR parameter %q, want exactly 2 digits", h.name, h.code)
		}
	}
}

func TestDimKeepsLeadingZero(t *testing.T) {
	if got, want := Dim("-", true), "\x1b[02m-\x1b[0m"; got != want {
		t.Errorf("Dim = %q, want %q (leading zero keeps the code 2 digits)", got, want)
	}
	if got := Dim("-", true); strings.Contains(got, "\x1b[2m") {
		t.Errorf("Dim = %q, want the zero-padded form, not the 1-digit form", got)
	}
	if len(Dim("x", true)) != len(Neutral("x", true)) {
		t.Error("Dim and Neutral must have equal escape-code overhead")
	}
}

func TestColorizeWrapsPayloadVerbatim(t *testing.T) {
	if got, want := colorize("36", "", true), "\x1b[36m\x1b[0m"; got != want {
		t.Errorf("colorize empty = %q, want %q", got, want)
	}
	if got, want := colorize("36", "a\tb\n", true), "\x1b[36ma\tb\n\x1b[0m"; got != want {
		t.Errorf("colorize = %q, want %q", got, want)
	}
	if got, want := colorize("36", "s", false), "s"; got != want {
		t.Errorf("colorize disabled = %q, want %q", got, want)
	}
}

func TestNeutralIsVisuallyANoOp(t *testing.T) {
	got := Neutral("cell", true)
	if !strings.Contains(got, "\x1b[39m") {
		t.Errorf("Neutral = %q, want the default-foreground code 39", got)
	}
	if stripped := strings.NewReplacer("\x1b[39m", "", "\x1b[0m", "").Replace(got); stripped != "cell" {
		t.Errorf("Neutral stripped = %q, want %q", stripped, "cell")
	}
}
