package present

import "testing"

func TestParse(t *testing.T) {
	for _, ok := range []Format{Color, Structured, JSON} {
		if got, err := Parse(string(ok)); err != nil || got != ok {
			t.Errorf("Parse(%q) = %q, %v; want %q, nil", ok, got, err, ok)
		}
	}
	if _, err := Parse("yaml"); err == nil {
		t.Error("Parse(\"yaml\") should error")
	}
}

func TestColorGate(t *testing.T) {
	cases := []struct {
		isTerminal, noColorSet bool
		want                   bool
	}{
		{isTerminal: true, noColorSet: false, want: true},
		{isTerminal: false, noColorSet: false, want: false},
		{isTerminal: true, noColorSet: true, want: false},
		{isTerminal: false, noColorSet: true, want: false},
	}
	for _, c := range cases {
		if got := ColorGate(c.isTerminal, c.noColorSet); got != c.want {
			t.Errorf("ColorGate(%v, %v) = %v, want %v", c.isTerminal, c.noColorSet, got, c.want)
		}
	}
}

func TestResolveDefault(t *testing.T) {
	// --format omitted: TTY on/off, NO_COLOR set/unset (via colorAllowed).
	if got := Resolve("", true); got != Color {
		t.Errorf("Resolve(\"\", true) = %q, want %q", got, Color)
	}
	if got := Resolve("", false); got != Structured {
		t.Errorf("Resolve(\"\", false) = %q, want %q", got, Structured)
	}
}

func TestResolveExplicitGating(t *testing.T) {
	// Colour gating always wins, even over an explicit --format color.
	if got := Resolve(Color, false); got != Structured {
		t.Errorf("Resolve(Color, false) = %q, want %q (gated)", got, Structured)
	}
	if got := Resolve(Color, true); got != Color {
		t.Errorf("Resolve(Color, true) = %q, want %q", got, Color)
	}
	if got := Resolve(Structured, true); got != Structured {
		t.Errorf("Resolve(Structured, true) = %q, want %q", got, Structured)
	}
	if got := Resolve(JSON, false); got != JSON {
		t.Errorf("Resolve(JSON, false) = %q, want %q (json is never gated)", got, JSON)
	}
}
