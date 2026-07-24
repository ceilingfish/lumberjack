package color

import (
	"bytes"
	"regexp"
	"testing"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestTableDisabledMatchesPlainTabwriter(t *testing.T) {
	withEnabled(t, false)

	var got bytes.Buffer
	tbl := NewTable(&got)
	tbl.Row("NAME\tACTION\tOTHER\n")
	tbl.Row("aaa\t%s\tzzz\n", tbl.Paint(Action, "checked out"))
	tbl.Row("aaa\t%s\tzzz\n", tbl.Paint(Action, "adopted"))
	if err := tbl.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Baseline: the exact same rows rendered with a plain tabwriter and no
	// colourisation at all.
	var want bytes.Buffer
	plain := NewTable(&want) // colour disabled, so this is the plain path too
	plain.Row("NAME\tACTION\tOTHER\n")
	plain.Row("aaa\t%s\tzzz\n", "checked out")
	plain.Row("aaa\t%s\tzzz\n", "adopted")
	if err := plain.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got.String() != want.String() {
		t.Errorf("disabled output diverged from plain baseline:\ngot:  %q\nwant: %q", got.String(), want.String())
	}
}

func TestTableEnabledStaysAligned(t *testing.T) {
	withEnabled(t, true)

	var colored bytes.Buffer
	tbl := NewTable(&colored)
	tbl.Row("NAME\tACTION\tOTHER\n")
	tbl.Row("aaa\t%s\tzzz\n", tbl.Paint(Action, "checked out"))
	tbl.Row("aaa\t%s\tzzz\n", tbl.Paint(Action, "adopted"))
	tbl.Row("aaa\t%s\tzzz\n", tbl.Paint(Action, "updated"))
	if err := tbl.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if !ansiRE.MatchString(colored.String()) {
		t.Fatalf("expected ANSI codes in output, got %q", colored.String())
	}

	stripped := ansiRE.ReplaceAllString(colored.String(), "")

	withEnabled(t, false)
	var plain bytes.Buffer
	ptbl := NewTable(&plain)
	ptbl.Row("NAME\tACTION\tOTHER\n")
	ptbl.Row("aaa\t%s\tzzz\n", "checked out")
	ptbl.Row("aaa\t%s\tzzz\n", "adopted")
	ptbl.Row("aaa\t%s\tzzz\n", "updated")
	if err := ptbl.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if stripped != plain.String() {
		t.Errorf("colourised output misaligned once ANSI codes are stripped:\ngot:  %q\nwant: %q", stripped, plain.String())
	}
}

func TestTableEnabledMixedDeEmphasis(t *testing.T) {
	// A column mixing a coloured "-" placeholder with plain values must still
	// align once colour codes are stripped.
	withEnabled(t, true)
	var colored bytes.Buffer
	tbl := NewTable(&colored)
	tbl.Row("aaa\t%s\n", tbl.Paint(Dim, "-"))
	tbl.Row("aaa\t%s\n", "#123456")
	if err := tbl.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	stripped := ansiRE.ReplaceAllString(colored.String(), "")

	withEnabled(t, false)
	var plain bytes.Buffer
	ptbl := NewTable(&plain)
	ptbl.Row("aaa\t%s\n", "-")
	ptbl.Row("aaa\t%s\n", "#123456")
	if err := ptbl.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if stripped != plain.String() {
		t.Errorf("mixed coloured/plain column misaligned:\ngot:  %q\nwant: %q", stripped, plain.String())
	}
}
