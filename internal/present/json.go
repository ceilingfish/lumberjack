package present

import (
	"bytes"
	"encoding/json"
	"io"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// protoMarshal renders a proto.Message the way the json Format always does:
// camelCase field names and enums by name (protojson's defaults), not the
// snake_case `json:"..."` tags protoc-gen-go emits for encoding/json.
var protoMarshal = protojson.Marshal

// WriteJSONList writes items as a bare JSON array (no envelope), one element
// per item, via protojson. A nil or empty slice writes "[]", never "null".
func WriteJSONList[T proto.Message](w io.Writer, items []T) error {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := protoMarshal(it)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	buf.WriteString("]\n")
	_, err := w.Write(buf.Bytes())
	return err
}

// WriteJSONArray writes items as a bare JSON array via encoding/json, for
// view-model element types (plain structs with their own `json:"lowerCamel"`
// tags) rather than proto messages — see WriteJSONList for those. A nil or
// empty slice writes "[]", never "null".
func WriteJSONArray[T any](w io.Writer, items []T) error {
	if items == nil {
		items = []T{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// WriteJSONObject writes v as a bare JSON object (no envelope): a
// proto.Message via protojson, or any other value — a view model, used only
// where a proto type doesn't carry everything the output needs, with its own
// `json:"lowerCamel"` tags — via encoding/json.
func WriteJSONObject(w io.Writer, v any) error {
	var b []byte
	var err error
	if m, ok := v.(proto.Message); ok {
		b, err = protoMarshal(m)
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ProtoRaw marshals a proto.Message with protojson for embedding inside a
// composite view-model struct: encoding/json passes a json.RawMessage field
// through verbatim, so the proto content keeps its protojson rendering.
func ProtoRaw(m proto.Message) (json.RawMessage, error) {
	b, err := protoMarshal(m)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
