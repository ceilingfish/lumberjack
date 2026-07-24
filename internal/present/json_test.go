package present

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func TestWriteJSONListEmptyIsBareArray(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSONList[*lumberjackv1.Repository](&buf, nil); err != nil {
		t.Fatalf("WriteJSONList: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("empty list = %q, want []", got)
	}
}

func TestWriteJSONListCamelCase(t *testing.T) {
	var buf bytes.Buffer
	repos := []*lumberjackv1.Repository{
		{DirPrefix: "n", LocalPath: "/p/n"},
	}
	if err := WriteJSONList(&buf, repos); err != nil {
		t.Fatalf("WriteJSONList: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 element, got %d", len(decoded))
	}
	// protojson renders proto3 field local_path as camelCase localPath.
	if decoded[0]["localPath"] != "/p/n" {
		t.Errorf("expected camelCase localPath key, got %v", decoded[0])
	}
	if _, hasSnake := decoded[0]["local_path"]; hasSnake {
		t.Errorf("output should not contain snake_case keys: %v", decoded[0])
	}
}

func TestWriteJSONObjectProto(t *testing.T) {
	var buf bytes.Buffer
	repo := &lumberjackv1.Repository{DirPrefix: "n", LocalPath: "/p/n"}
	if err := WriteJSONObject(&buf, repo); err != nil {
		t.Fatalf("WriteJSONObject: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if decoded["localPath"] != "/p/n" {
		t.Errorf("expected camelCase localPath key, got %v", decoded)
	}
}

func TestWriteJSONObjectViewModel(t *testing.T) {
	var buf bytes.Buffer
	type msg struct {
		Message string `json:"message"`
	}
	if err := WriteJSONObject(&buf, msg{Message: "hello"}); err != nil {
		t.Fatalf("WriteJSONObject: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != `{"message":"hello"}` {
		t.Errorf("got %q", got)
	}
}
