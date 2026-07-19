package color

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Table renders a tab-aligned table on top of text/tabwriter whose cells may
// be colourised without breaking column alignment.
//
// text/tabwriter counts every byte toward a column's width, including
// invisible ANSI escape sequences, so naively writing colourised cells into
// it misaligns columns whenever cells in the same column carry different
// amounts of (invisible) colour-code overhead. Table avoids this by always
// computing alignment from the plain, uncoloured cell text — via Paint,
// which hands tabwriter the plain value and remembers how to colourise it —
// then applying colour to the already-aligned output as a final pass. Since
// that pass only wraps existing visible text in invisible escape codes, it
// cannot change any column's width.
//
// When colour is disabled, Table writes straight through to tabwriter with no
// buffering or post-processing, so output is byte-for-byte identical to a
// plain tabwriter render.
type Table struct {
	tw      *tabwriter.Writer
	dst     io.Writer
	buf     *bytes.Buffer // non-nil only when colour is enabled
	err     error
	paints  [][]paintOp // one entry per Row call, in order
	pending []paintOp   // accumulated by Paint, consumed by the next Row
}

type paintOp struct {
	value string
	fn    func(string) string
}

// NewTable starts a new table writer over w, matching the column layout the
// render helpers have always used (tab-separated, 2-space padding).
func NewTable(w io.Writer) *Table {
	t := &Table{dst: w}
	if Enabled() {
		t.buf = &bytes.Buffer{}
		t.tw = tabwriter.NewWriter(t.buf, 0, 0, 2, ' ', 0)
	} else {
		t.tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	}
	return t
}

// Paint marks s to be colourised with fn once the table is aligned, and
// returns s unchanged so tabwriter's width computation always sees plain
// text. Use it inline as a Row argument, e.g.
// t.Row("%s\t%s\n", t.Paint(color.Branch, name), t.Paint(color.Action, verb)).
func (t *Table) Paint(fn func(string) string, s string) string {
	if s != "" {
		t.pending = append(t.pending, paintOp{value: s, fn: fn})
	}
	return s
}

// Row writes one tab-terminated row (format must end in "\n", and must not
// contain any other newline) and associates it with any Paint calls made
// while building its arguments.
func (t *Table) Row(format string, args ...any) {
	if t.err != nil {
		return
	}
	rowPaints := t.pending
	t.pending = nil
	if _, err := fmt.Fprintf(t.tw, format, args...); err != nil {
		t.err = err
		return
	}
	t.paints = append(t.paints, rowPaints)
}

// Flush writes the buffered table and returns the first error seen.
func (t *Table) Flush() error {
	if t.err != nil {
		return t.err
	}
	if err := t.tw.Flush(); err != nil {
		return err
	}
	if t.buf == nil {
		return nil // colour disabled: already written straight to dst
	}
	lines := strings.Split(t.buf.String(), "\n")
	for i, paints := range t.paints {
		if i < len(lines) {
			lines[i] = applyPaints(lines[i], paints)
		}
	}
	_, err := io.WriteString(t.dst, strings.Join(lines, "\n"))
	return err
}

// applyPaints colourises each recorded value in line, in order, searching
// left to right from where the previous match ended so a value that recurs
// elsewhere on the line can't be matched out of turn.
func applyPaints(line string, paints []paintOp) string {
	if len(paints) == 0 {
		return line
	}
	var b strings.Builder
	cur := 0
	for _, p := range paints {
		idx := strings.Index(line[cur:], p.value)
		if idx < 0 {
			continue // value not found (shouldn't happen) — leave line as is
		}
		idx += cur
		b.WriteString(line[cur:idx])
		b.WriteString(p.fn(p.value))
		cur = idx + len(p.value)
	}
	b.WriteString(line[cur:])
	return b.String()
}
