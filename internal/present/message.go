package present

import (
	"fmt"
	"io"
)

// Message is the json Format view model for commands that report a single
// human-readable outcome with no proto type behind it.
type Message struct {
	Message string `json:"message"`
}

// WriteMessage writes msg per format: the bare JSON view model, or plain text
// (color and structured render identically — there is nothing to colourise in
// a one-line outcome).
func WriteMessage(w io.Writer, format Format, msg string) error {
	if format == JSON {
		return WriteJSONObject(w, Message{Message: msg})
	}
	_, err := fmt.Fprintln(w, msg)
	return err
}
