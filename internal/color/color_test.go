package color

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		name       string
		isTerminal bool
		noColorSet bool
		want       bool
	}{
		{"tty, no NO_COLOR", true, false, true},
		{"tty, NO_COLOR set", true, true, false},
		{"not a tty, no NO_COLOR", false, false, false},
		{"not a tty, NO_COLOR set", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.isTerminal, c.noColorSet); got != c.want {
				t.Errorf("Decide(%v, %v) = %v, want %v", c.isTerminal, c.noColorSet, got, c.want)
			}
		})
	}
}

// withEnabled overrides Enabled for the duration of the test.
func withEnabled(t *testing.T, v bool) {
	t.Helper()
	prev := Enabled
	Enabled = func() bool { return v }
	t.Cleanup(func() { Enabled = prev })
}

func TestCategoryFunctionsDisabled(t *testing.T) {
	withEnabled(t, false)
	for _, fn := range []func(string) string{Action, Path, Branch, OK, Warning, Error, Dim} {
		if got := fn("hello"); got != "hello" {
			t.Errorf("got %q, want unchanged %q", got, "hello")
		}
	}
	if got := Action(""); got != "" {
		t.Errorf("empty string should stay empty, got %q", got)
	}
}

func TestCategoryFunctionsEnabled(t *testing.T) {
	withEnabled(t, true)
	got := Action("checked out")
	want := "\x1b[36mchecked out\x1b[0m"
	if got != want {
		t.Errorf("Action(...) = %q, want %q", got, want)
	}
	// Colourising an empty string is still a no-op even when enabled.
	if got := Path(""); got != "" {
		t.Errorf("empty string should stay empty, got %q", got)
	}
}
