package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type document struct {
	Resource map[string]any `json:"resource"`
	Scope    map[string]any `json:"scope"`
	Record   map[string]any `json:"record"`
}

// decodeJSON preserves OTLP int64 values and rejects trailing JSON. Use this
// wherever untyped telemetry is decoded; float64 silently rounds large counters.
func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeDocument(raw json.RawMessage) (document, error) {
	var d document
	err := decodeJSON(raw, &d)
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
		if decodeJSON([]byte(s), &out) == nil {
			return out
		}
	}
	return v
}
