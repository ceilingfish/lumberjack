package present

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteMessageJSON(t *testing.T) {
	var out bytes.Buffer
	if err := WriteMessage(&out, JSON, "hello"); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if !strings.Contains(out.String(), `"message"`) || !strings.Contains(out.String(), "hello") {
		t.Errorf("out = %q, want a JSON view model", out.String())
	}
}
