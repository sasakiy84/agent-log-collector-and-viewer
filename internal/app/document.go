package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type document struct {
	Resource map[string]any `json:"resource"`
	Scope    map[string]any `json:"scope"`
	Record   map[string]any `json:"record"`
}

func decodeDocument(raw json.RawMessage) (document, error) {
	var d document
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&d)
	return d, err
}
func anyValue(v any) any {
	x, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if a, ok := x["arrayValue"]; ok {
		values := []any{}
		for _, v := range arr(obj(a)["values"]) {
			values = append(values, anyValue(v))
		}
		return values
	}
	if a, ok := x["kvlistValue"]; ok {
		return decodedAttributes(obj(a)["values"])
	}
	for _, k := range []string{"stringValue", "intValue", "doubleValue", "boolValue", "bytesValue"} {
		if v, ok := x[k]; ok {
			return v
		}
	}
	return v
}
func decodedAttributes(items any) map[string]any {
	out := map[string]any{}
	for _, v := range arr(items) {
		a := obj(v)
		out[str(a["key"])] = anyValue(a["value"])
	}
	return out
}
func (d document) attributes() map[string]any {
	out := map[string]any{}
	for _, scope := range []map[string]any{d.Resource, d.Scope, d.Record} {
		for k, v := range decodedAttributes(scope["attributes"]) {
			out[k] = v
		}
	}
	return out
}
func present(v any) bool { return v != nil && v != "" }
func firstValue(a map[string]any, keys ...string) any {
	for _, k := range keys {
		if v := a[k]; present(v) {
			return v
		}
	}
	return nil
}
func display(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
func decoded(v any) any {
	if s, ok := v.(string); ok {
		var out any
		d := json.NewDecoder(bytes.NewBufferString(s))
		d.UseNumber()
		if d.Decode(&out) == nil {
			var tail any
			if d.Decode(&tail) == io.EOF {
				return out
			}
		}
	}
	return v
}
