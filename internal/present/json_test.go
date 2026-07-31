package present

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

var errWrite = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func invalidUTF8Repo() *lumberjackv1.Repository {
	return &lumberjackv1.Repository{DirPrefix: "\xff\xfe"}
}

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
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("output %q should end with a newline", buf.String())
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

func TestWriteJSONListSeparatesElementsAndEndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	repos := []*lumberjackv1.Repository{
		{DirPrefix: "a", LocalPath: "/p/a"},
		{DirPrefix: "b", LocalPath: "/p/b"},
	}
	if err := WriteJSONList(&buf, repos); err != nil {
		t.Fatalf("WriteJSONList: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("output %q should end with a newline", buf.String())
	}
	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 elements, got %d (%q)", len(decoded), buf.String())
	}
	if decoded[0]["dirPrefix"] != "a" || decoded[1]["dirPrefix"] != "b" {
		t.Errorf("elements out of order or malformed: %v", decoded)
	}
}

func TestWriteJSONListMarshalError(t *testing.T) {
	var buf bytes.Buffer
	err := WriteJSONList(&buf, []*lumberjackv1.Repository{invalidUTF8Repo()})
	if err == nil {
		t.Fatalf("expected an error for an unmarshalable message, wrote %q", buf.String())
	}
}

func TestWriteJSONListWriteError(t *testing.T) {
	err := WriteJSONList(failingWriter{}, []*lumberjackv1.Repository{{DirPrefix: "a"}})
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want %v", err, errWrite)
	}
}

func TestWriteJSONArrayNilIsBareArray(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSONArray[struct{}](&buf, nil); err != nil {
		t.Fatalf("WriteJSONArray: %v", err)
	}
	if got := buf.String(); got != "[]\n" {
		t.Errorf("nil slice = %q, want \"[]\\n\"", got)
	}
}

func TestWriteJSONArrayUsesStructTags(t *testing.T) {
	type row struct {
		LocalPath string `json:"localPath"`
	}
	var buf bytes.Buffer
	if err := WriteJSONArray(&buf, []row{{LocalPath: "/p/a"}, {LocalPath: "/p/b"}}); err != nil {
		t.Fatalf("WriteJSONArray: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("output %q should end with a newline", buf.String())
	}
	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(decoded) != 2 || decoded[0]["localPath"] != "/p/a" || decoded[1]["localPath"] != "/p/b" {
		t.Errorf("decoded = %v", decoded)
	}
}

func TestWriteJSONArrayMarshalError(t *testing.T) {
	var buf bytes.Buffer
	type row struct {
		Ch chan int `json:"ch"`
	}
	if err := WriteJSONArray(&buf, []row{{}}); err == nil {
		t.Fatalf("expected an error for an unmarshalable element, wrote %q", buf.String())
	}
}

func TestWriteJSONArrayWriteError(t *testing.T) {
	if err := WriteJSONArray(failingWriter{}, []int{1}); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want %v", err, errWrite)
	}
}

func TestWriteJSONObjectProtoMarshalError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSONObject(&buf, invalidUTF8Repo()); err == nil {
		t.Fatalf("expected an error for an unmarshalable proto message, wrote %q", buf.String())
	}
}

func TestWriteJSONObjectViewModelMarshalError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSONObject(&buf, make(chan int)); err == nil {
		t.Fatalf("expected an error for an unmarshalable value, wrote %q", buf.String())
	}
}

func TestWriteJSONObjectWriteError(t *testing.T) {
	if err := WriteJSONObject(failingWriter{}, map[string]string{"a": "b"}); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want %v", err, errWrite)
	}
}

func TestProtoRawEmbedsProtojsonRendering(t *testing.T) {
	raw, err := ProtoRaw(&lumberjackv1.Repository{DirPrefix: "n", LocalPath: "/p/n"})
	if err != nil {
		t.Fatalf("ProtoRaw: %v", err)
	}
	var buf bytes.Buffer
	type composite struct {
		Repository json.RawMessage `json:"repository"`
		Extra      string          `json:"extra"`
	}
	if err := WriteJSONObject(&buf, composite{Repository: raw, Extra: "e"}); err != nil {
		t.Fatalf("WriteJSONObject: %v", err)
	}
	var decoded struct {
		Repository map[string]any `json:"repository"`
		Extra      string         `json:"extra"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if decoded.Extra != "e" {
		t.Errorf("extra = %q, want %q", decoded.Extra, "e")
	}
	if decoded.Repository["localPath"] != "/p/n" {
		t.Errorf("embedded proto lost its protojson camelCase rendering: %v", decoded.Repository)
	}
	if _, hasSnake := decoded.Repository["local_path"]; hasSnake {
		t.Errorf("embedded proto should not contain snake_case keys: %v", decoded.Repository)
	}
}

func TestProtoRawMarshalError(t *testing.T) {
	raw, err := ProtoRaw(invalidUTF8Repo())
	if err == nil {
		t.Fatalf("expected an error for an unmarshalable message, got %q", raw)
	}
	if raw != nil {
		t.Errorf("raw = %q, want nil on error", raw)
	}
}
