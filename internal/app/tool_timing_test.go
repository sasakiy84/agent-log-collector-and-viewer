package app

import (
	"os"
	"strings"
	"testing"
)

func toolTimingFixture(t *testing.T) Event {
	t.Helper()
	raw, err := os.ReadFile("testdata/codex-tool-dispatch.json")
	if err != nil {
		t.Fatal(err)
	}
	return Event{ID: "dispatch", Session: "codex:fixture-session", Source: "codex", Signal: "traces", Name: "exec", Time: "2026-11-15T15:14:11.117470827Z", Trace: strings.Repeat("a", 32), Span: strings.Repeat("b", 16), ParentSpan: strings.Repeat("c", 16), Raw: raw}
}

func TestCodexToolTimingIsNotOutput(t *testing.T) {
	a := setup(t)
	span := toolTimingFixture(t)
	start := recorded(t, "input", "import", "codex", "fixture-session", "gen_ai.tool.start", map[string]any{"call_id": "fixture-call", "tool_name": "exec", "arguments": `{"cmd":"echo hello"}`})
	result := recorded(t, "output", "import", "codex", "fixture-session", "gen_ai.tool.result", map[string]any{"call_id": "fixture-call", "tool_name": "exec", "output": "hello"})
	save(t, a, start, result, span)
	check := func() {
		t.Helper()
		var p SessionPage
		getJSON(t, a, "/api/session?id=codex:fixture-session&view=activity", &p)
		if p.Count != 2 {
			t.Fatal("timing span leaked into activity", p.Count)
		}
		var logs processingPage
		getJSON(t, a, "/api/processing?session=codex:fixture-session", &logs)
		if len(logs.Events) != 2 {
			t.Fatal("timing span leaked into normal processing", len(logs.Events))
		}
		getJSON(t, a, "/api/processing?session=codex:fixture-session&diagnostics=1", &logs)
		if len(logs.Events) != 3 {
			t.Fatal("diagnostics lost the original timing event", logs)
		}
		e := inspected(t, a, span.Session, span.ID, "")
		if e.Detail.Kind != "event" || e.Detail.Title != "Tool execution timing" || !e.Detail.Diagnostic || e.Detail.ToolPhase != "" || e.ToolPair != nil {
			t.Fatal("timing presented as output", e)
		}
		if len(e.Detail.Blocks) != 0 || !hasField(e.Detail, "Phase", "dispatch") || !hasField(e.Detail, "Span duration", "5216.746042 ms") {
			t.Fatal("missing timing context or fake content", e.Detail)
		}
		if pair := inspected(t, a, span.Session, start.ID, "").ToolPair; pair.State != "paired" || pair.Counterpart.ID != result.ID {
			t.Fatal("timing span polluted the actual output link", pair)
		}
	}
	check()

}

func TestCodexToolTimingKeepsContentFailuresAndOtherProviders(t *testing.T) {
	for _, test := range []struct {
		name, source string
		attributes   map[string]any
		outputEvent  bool
		failure      bool
		wantKind     string
		wantDiag     bool
	}{
		{"exec_command", "codex", nil, false, false, "event", true},
		{"exec_command", "codex", map[string]any{"arguments": `{"cmd":"pwd"}`}, false, false, "tool", false},
		{"exec_command", "codex", nil, true, false, "tool", false},
		{"exec_command", "codex", nil, false, true, "event", true},
		{"codex.tool_result", "codex", nil, false, true, "tool", true},
		{"codex.tool_decision", "codex", map[string]any{"decision": "approved"}, false, false, "event", true},
		{"exec_command", "claude", nil, false, false, "tool", false},
		{"execute_tool", "copilot", map[string]any{"gen_ai.operation.name": "execute_tool"}, false, false, "tool", false},
	} {
		t.Run(test.source+"/"+test.name, func(t *testing.T) {
			at := map[string]any{"tool_name": "exec_command", "call_id": "call"}
			for k, v := range test.attributes {
				at[k] = v
			}
			e := modelSpan(t, "test", "session", "span", "", test.name, at)
			e.Source = test.source
			doc, _ := decodeDocument(e.Raw)
			if test.outputEvent {
				doc.Record["events"] = []any{map[string]any{"name": "tool.output", "attributes": []any{map[string]any{"key": "output", "value": map[string]any{"stringValue": "real nested output"}}}}}
			}
			if test.failure {
				doc.Record["status"] = map[string]any{"code": "STATUS_CODE_ERROR", "message": "execution failed"}
			}
			d := describe(e, doc)
			if d.Kind != test.wantKind || d.Diagnostic != test.wantDiag || d.Error != test.failure || test.failure && !detailActivity(d) {
				t.Fatal("content, failure or provider misclassified", d)
			}
			if test.outputEvent && !hasBlock(d, "Output", "real nested output") {
				t.Fatal("nested tool output hidden", d)
			}
			if test.name == "codex.tool_result" && (d.ToolPhase != "tool.result" || len(correlationKeys(e, d, doc)) != 1) {
				t.Fatal("explicit failure lost its real output identity", d)
			}
		})
	}
	// Imported events with no body remain explicitly imported tool records.
	e := toolTimingFixture(t)
	e.Origin = "import"
	doc, _ := decodeDocument(e.Raw)
	if d := describe(e, doc); d.Kind != "tool" || d.Diagnostic {
		t.Fatal("imported tool event reclassified", d)
	}
}
