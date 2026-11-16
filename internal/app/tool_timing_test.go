package app

import (
	"context"
	"database/sql"
	"encoding/json"
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

	// Simulate v0.20.0 after its conversation/Guardian job has completed. Its
	// timing span was falsely merged into the imported output using the call ID.
	doc, _ := decodeDocument(span.Raw)
	old := describe(span, doc)
	old.Kind, old.Title, old.ToolPhase, old.Diagnostic = "tool", "exec", "tool.result", false
	old.Blocks = []Block{{Label: "Details", Text: "Tool content not exported"}}
	encoded, _ := json.Marshal(cardDetail(old))
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE event_cards SET kind='tool',activity=1,detail=? WHERE id=?", encoded, span.ID); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO event_keys VALUES(?,'tool.call',?)", span.ID, identityKey("", "tool.result", "tool.call", "fixture-call")); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO read_model_dirty VALUES(?,?),(?,?)", span.Session, span.ID, result.Session, result.ID); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	if p := page(t, a, span.Session, "", 0); p.Count != 2 {
		t.Fatal("old false merge not reproduced", p.Count)
	}
	if _, err := a.DB.Exec("UPDATE maintenance_jobs SET cursor='',complete=0,processed=0 WHERE name=?", toolTimingJob); err != nil {
		t.Fatal(err)
	}
	if err := a.runMaintenance(context.Background(), toolTimingJob, backfillToolTiming, 0); err != nil {
		t.Fatal(err)
	}
	check()
	var raw []byte
	if err := a.DB.QueryRow("SELECT raw FROM events WHERE id=?", span.ID).Scan(&raw); err != nil || string(raw) != string(span.Raw) {
		t.Fatal("repair changed original data", err)
	}
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
