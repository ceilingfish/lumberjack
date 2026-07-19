package present

// Colour helper — standard 8/16-colour ANSI SGR codes (no truecolour), per
// the palette settled in #2. #3 depends on this and #2 had not landed when
// this was implemented, so the helper lives here; #2 should consume it rather
// than reimplementing it (see the issue's sequencing note).
//
// Each function takes the enabled flag explicitly rather than reading global
// state, so callers (the render helpers) decide once per invocation and every
// cell in a table column gets the same, constant-length escape-code overhead
// — required for text/tabwriter to keep columns aligned, since it counts
// escape bytes toward cell width (see #2's "tabwriter alignment" constraint).
const (
	ansiAction  = "36" // cyan — action verbs
	ansiPath    = "34" // blue — file-system paths
	ansiBranch  = "32" // green — git branch names
	ansiOK      = "32" // green — status: ok
	ansiWarn    = "33" // yellow — status: warning
	ansiErr     = "31" // red — status: error
	ansiDim     = "02" // dim — de-emphasis (e.g. "-" / "never"); the leading
	// zero keeps every SGR code in this package exactly 2 digits (terminals
	// parse SGR parameters as plain decimal numbers, so "02" behaves
	// identically to "2") — see the invariant documented above.
	ansiNeutral = "39" // default foreground — a visual no-op used purely to
	// keep a cell's escape-code overhead equal to its colourised siblings
	// in the same table column.
)

func colorize(code, s string, enabled bool) string {
	if !enabled {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Action colourises an action verb (checked out / adopted / deleted / ...).
func Action(s string, enabled bool) string { return colorize(ansiAction, s, enabled) }

// Path colourises a file-system path.
func Path(s string, enabled bool) string { return colorize(ansiPath, s, enabled) }

// Branch colourises a git branch name.
func Branch(s string, enabled bool) string { return colorize(ansiBranch, s, enabled) }

// StatusOK colourises an "ok" status.
func StatusOK(s string, enabled bool) string { return colorize(ansiOK, s, enabled) }

// StatusWarn colourises a warning status.
func StatusWarn(s string, enabled bool) string { return colorize(ansiWarn, s, enabled) }

// StatusErr colourises an error status.
func StatusErr(s string, enabled bool) string { return colorize(ansiErr, s, enabled) }

// Dim de-emphasises a value such as "-" or "never".
func Dim(s string, enabled bool) string { return colorize(ansiDim, s, enabled) }

// Neutral wraps a value with a no-op colour (default foreground) so it
// carries the same escape-code overhead as its colourised column siblings,
// without visually changing it.
func Neutral(s string, enabled bool) string { return colorize(ansiNeutral, s, enabled) }
