// Package present is the presentation layer between commands and stdout.
// Commands hand it data (proto types from pkg/client, or a small view model
// where a proto type does not carry everything the output needs) and a
// target Format; it renders per the rules in the "Add a generic output
// formatter" design (see issue #3): color and structured are human-readable
// (the latter always monochrome), json is machine-readable and stable.
package present

import "fmt"

// Format selects how a command's output is rendered.
type Format string

const (
	// Color is human-readable output colourised per the palette in #2.
	Color Format = "color"
	// Structured is human-readable output with no colour — today's plain
	// tabular/detail rendering.
	Structured Format = "structured"
	// JSON is machine-readable: camelCase keys, lists as bare arrays, single
	// items as bare objects, no envelope.
	JSON Format = "json"
)

// Parse validates a --format flag value, rejecting anything unknown with a
// clear error.
func Parse(s string) (Format, error) {
	switch Format(s) {
	case Color, Structured, JSON:
		return Format(s), nil
	default:
		return "", fmt.Errorf("invalid --format %q: must be one of %s, %s, %s", s, Color, Structured, JSON)
	}
}

// ColorGate reports whether colour output is permitted: the destination must
// be an interactive terminal and NO_COLOR must be unset. NO_COLOR is
// presence-based per the no-color.org convention — any value disables colour,
// including an empty one — so callers pass whether the variable is set, not
// its value.
func ColorGate(isTerminal, noColorSet bool) bool {
	return isTerminal && !noColorSet
}

// Resolve applies the default-resolution and colour-gating rules to an
// explicit --format value (empty when the flag was omitted):
//   - omitted resolves to Color when colorAllowed, else Structured;
//   - an explicit Color downgrades to Structured when colorAllowed is false
//     (colour gating always wins, even over an explicit --format color);
//   - any other explicit value passes through unchanged.
func Resolve(explicit Format, colorAllowed bool) Format {
	if explicit == "" {
		if colorAllowed {
			return Color
		}
		return Structured
	}
	if explicit == Color && !colorAllowed {
		return Structured
	}
	return explicit
}
