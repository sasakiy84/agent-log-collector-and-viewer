package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

func detailFixture(t *testing.T, name, signal string, at, r map[string]any) Detail {
	t.Helper()
	if r == nil {
		r = map[string]any{}
	}
	values := []any{}
	for k, v := range at {
		values = append(values, map[string]any{"key": k, "value": map[string]any{"stringValue": v}})
	}
	r["attributes"] = values
	raw, err := json.Marshal(map[string]any{"record": r})
	if err != nil {
		t.Fatal(err)
	}
	d, err := decodeDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	return describe(Event{Name: name, Signal: signal}, d)
}
func hasField(p Detail, k, v string) bool {
	for _, f := range p.Fields {
		if f[0] == k && f[1] == v {
			return true
		}
	}
	return false
}
func hasBlock(p Detail, label, text string) bool {
	for _, b := range p.Blocks {
		if b.Label == label && b.Text == text {
			return true
		}
	}
	return false
}
func TestAgentDetails(t *testing.T) {
	p := detailFixture(t, "logs", "logs", map[string]any{"event.name": "user_prompt", "prompt_text": "Review API"}, nil)
	if p.Kind != "user" || !hasBlock(p, "Request", "Review API") {
		t.Fatal(p)
	}
	p = detailFixture(t, "logs", "logs", map[string]any{"event.name": "tool_result", "tool_name": "Bash", "tool_parameters": `{"full_command":"go test ./..."}`, "success": "false", "error": "failed", "duration_ms": "1200"}, nil)
	if p.Title != "Bash" || !p.Error || !hasBlock(p, "Command", "go test ./...") || !hasField(p, "Duration", "1.20 s") {
		t.Fatal(p)
	}
	p = detailFixture(t, "logs", "logs", map[string]any{"event.name": "codex.tool_result", "tool_name": "exec_command", "arguments": `{"cmd":"ls"}`, "output": "README.md", "success": "true"}, nil)
	if p.Kind != "tool" || !hasBlock(p, "Output", "README.md") {
		t.Fatal(p)
	}
	p = detailFixture(t, "chat", "traces", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.input.messages": `[{"role":"user","parts":[{"type":"text","content":"hello"}]}]`, "gen_ai.usage.input_tokens": "0", "github.copilot.cost": "2"}, nil)
	if p.Kind != "model" || !hasBlock(p, "user", "hello") || !hasField(p, "Input tokens", "0") || !hasField(p, "Billing multiplier", "2") {
		t.Fatal(p)
	}
	p = detailFixture(t, "assistant_response", "logs", nil, nil)
	if !hasBlock(p, "Response", "Content not exported") {
		t.Fatal(p)
	}
	p = detailFixture(t, "tokens", "metrics", nil, map[string]any{"name": "tokens", "asInt": "300", "aggregationTemporality": "AGGREGATION_TEMPORALITY_CUMULATIVE"})
	if !hasField(p, "Temporality", "Cumulative") || !hasField(p, "Value", "300") {
		t.Fatal(p)
	}
	p = detailFixture(t, "execute_tool", "traces", map[string]any{"gen_ai.tool.name": "read_file"}, map[string]any{"events": []any{map[string]any{"name": "tool.output", "attributes": []any{map[string]any{"key": "tool.output", "value": map[string]any{"stringValue": "file content"}}}}}})
	if !hasBlock(p, "tool.output", "file content") {
		t.Fatal(p)
	}
}
func TestTypedValuesAndZero(t *testing.T) {
	value := anyValue(map[string]any{"arrayValue": map[string]any{"values": []any{map[string]any{"intValue": "9007199254740993"}, map[string]any{"boolValue": false}}}})
	if !reflect.DeepEqual(value, []any{"9007199254740993", false}) {
		t.Fatal(value)
	}
	p := detailFixture(t, "tool_result", "logs", map[string]any{"success": false, "duration_ms": 0, "gen_ai.usage.output_tokens": 0}, nil)
	if !p.Error || !hasField(p, "Duration", "0 ms") || !hasField(p, "Output tokens", "0") {
		t.Fatal(p)
	}
	p = detailFixture(t, "gen_ai.user.message", "logs", map[string]any{"gen_ai.input.messages": `[{"role":"user","parts":[{"type":"text","content":"Imported request"}]}]`, "service.namespace": "shop", "project.tags": `["backend"]`}, nil)
	if p.Project != "shop" || !hasBlock(p, "Request", "Imported request") {
		t.Fatal(p)
	}
}

func TestTextIsNeverPartiallyDecoded(t *testing.T) {
	for _, text := range []string{"123 hello world", "{\"ok\":true}\nadditional output", "null", `"quoted message"`} {
		p := detailFixture(t, "user_prompt", "logs", map[string]any{"prompt": text}, nil)
		if !hasBlock(p, "Request", text) {
			t.Fatalf("message changed: %q -> %+v", text, p.Blocks)
		}
	}
	for _, text := range []string{"{\"ok\":true}\nadditional output", "null", `"quoted output"`, "123 hello world"} {
		p := detailFixture(t, "tool_result", "logs", map[string]any{"output": text}, nil)
		if !hasBlock(p, "Output", text) {
			t.Fatal(p.Blocks)
		}
	}
}
func TestFalseErrorAndInformationalStatus(t *testing.T) {
	p := detailFixture(t, "tool_result", "logs", map[string]any{"error": false, "success": true}, nil)
	if p.Error || !hasField(p, "Result", "Succeeded") {
		t.Fatal(p)
	}
	p = detailFixture(t, "execute_tool", "traces", nil, map[string]any{"status": map[string]any{"code": 1, "message": "completed normally"}})
	if p.Error {
		t.Fatal(p)
	}
	p = detailFixture(t, "tool_result", "logs", map[string]any{"error": false, "error.message": "actual error"}, nil)
	if !p.Error || !hasBlock(p, "Error", "actual error") {
		t.Fatal(p)
	}
}
