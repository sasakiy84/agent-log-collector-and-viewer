package app

import (
	"context"
	"encoding/json"
	"testing"
)

func codeModeTestSpan(t *testing.T, id, name, trace string, recordFields map[string]any, extra ...map[string]string) Event {
	t.Helper()
	module := "codex_code_mode::grpc_session"
	if name == "code_mode.grpc.callback" {
		module += "::callbacks"
	}
	attribute := func(key, value string) map[string]any {
		return map[string]any{"key": key, "value": map[string]string{"stringValue": value}}
	}
	attributes := []any{attribute("code.module.name", module)}
	if name == "code_mode.grpc.callback" || name == "code_mode.grpc.subscribe_to_tool_calls" {
		attributes = append(attributes, attribute("session.id", "runtime-"+id))
	}
	for _, fields := range extra {
		for key, value := range fields {
			attributes = append(attributes, attribute(key, value))
		}
	}
	doc := document{
		Resource: map[string]any{"attributes": []any{attribute("service.name", "codex-app-server")}},
		Record:   map[string]any{"name": name, "kind": "SPAN_KIND_INTERNAL", "attributes": attributes},
	}
	for key, value := range recordFields {
		doc.Record[key] = value
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return Event{ID: id, Name: name, Origin: "otel", Source: "codex", Signal: "traces",
		Session: "codex:runtime-" + id, Trace: trace, Span: id, Time: "2026-11-21T02:00:00.000000000Z", Raw: raw}
}

func codeModeTransportFixtures(t *testing.T) []Event {
	t.Helper()
	callback := "code_mode.grpc.callback"
	subscribe := "code_mode.grpc.subscribe_to_tool_calls"
	completed := recorded(t, "completed", "otel", "codex", "chat", "codex.sse_event",
		map[string]any{"event.kind": "response.completed", "output_token_count": "10"})
	completed.Origin, completed.Trace = "otel", "conversation"
	nested := codeModeTestSpan(t, "nested-open", "code_mode.grpc.open_session", "nested", nil)
	nested.ParentSpan = "parent"
	return []Event{
		codeModeTestSpan(t, "callback", callback, "transport-1", nil),
		codeModeTestSpan(t, "subscription", subscribe, "transport-2", nil),
		codeModeTestSpan(t, "open-binding", "code_mode.grpc.open_binding", "transport-3", nil),
		codeModeTestSpan(t, "open-session", "code_mode.grpc.open_session", "transport-3", nil),
		codeModeTestSpan(t, "failed", callback, "failed", map[string]any{"status": map[string]any{"code": "STATUS_CODE_ERROR"}}),
		codeModeTestSpan(t, "with-usage", callback, "usage", nil, map[string]string{"output_token_count": "13"}),
		codeModeTestSpan(t, "with-event", callback, "event", map[string]any{"events": []any{map[string]any{"name": "exception"}}}),
		codeModeTestSpan(t, "mixed", callback, "conversation", nil),
		completed,
		nested,
	}
}

func TestCodeModeTransportExcludedBeforeInbox(t *testing.T) {
	a := setup(t)
	q, err := openIngestQueue(a.ctx, a.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.close() })
	a.inbox = q
	defer func() { a.inbox = nil }()
	if err = a.accept(context.Background(), codeModeTransportFixtures(t), false); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if err = q.db.QueryRow("SELECT data FROM ingest_batches").Scan(&data); err != nil {
		t.Fatal(err)
	}
	var events []Event
	if err = json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("unexpected inbox events: %d", len(events))
	}
	for _, e := range events {
		if e.ID == "callback" || e.ID == "subscription" || e.ID == "open-session" || e.ID == "open-binding" {
			t.Fatalf("stored transport event: %s", e.ID)
		}
	}
}

func TestCodeModeTransportCleanupRemovesStoredSpansAndProjections(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	events := codeModeTransportFixtures(t)
	// Seed the derived tables as an older binary would have, then restore
	// the trace signal that the old binary would have stored for these spans.
	for i := range events {
		events[i].Signal = "logs"
	}
	save(t, a, events...)
	if _, err := a.DB.Exec(undoDirectDeleteMigration + "UPDATE records SET signal='traces' WHERE name LIKE 'code_mode.grpc.%'; DELETE FROM schema_migrations WHERE version>=7"); err != nil {
		t.Fatal(err)
	}
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, a.DB, plan); err != nil {
		t.Fatal(err)
	}
	if err = a.prepareDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"SELECT COUNT(*) FROM records WHERE id IN ('callback','subscription','open-binding','open-session')",
		"SELECT COUNT(*) FROM search_documents WHERE event_id IN ('callback','subscription','open-binding','open-session')",
		"SELECT COUNT(*) FROM logical_events WHERE session IN ('codex:runtime-callback','codex:runtime-subscription','codex:runtime-open-binding','codex:runtime-open-session')",
		"SELECT COUNT(*) FROM sessions WHERE session IN ('codex:runtime-callback','codex:runtime-subscription','codex:runtime-open-binding','codex:runtime-open-session')",
		"SELECT COUNT(*) FROM session_versions WHERE session IN ('codex:runtime-callback','codex:runtime-subscription','codex:runtime-open-binding','codex:runtime-open-session')",
	} {
		var count int
		if err := a.DB.QueryRow(statement).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: %d, %v", statement, count, err)
		}
	}
	if got := countEvents(t, a); got != 6 {
		t.Fatalf("migration kept %d events, want 6", got)
	}
	usageWant(t, sessionUsage(t, a, "codex:chat", ""), "output_tokens", "10")
}
