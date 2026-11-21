package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFullTextSearchUsesConversationToolsAndErrors(t *testing.T) {
	a := setup(t)
	save(t, a,
		recorded(t, "user", "otel", "codex", "focused", "codex.user_prompt", map[string]any{
			"prompt":                "<environment_context>injectedcontext</environment_context> humanrequest",
			"gen_ai.input.messages": `[{"role":"system","content":"systemprompt"},{"role":"user","content":"replayedhistory"}]`,
			"thread.name":           "transportthread", "service.version": "sdkversion",
		}),
		recorded(t, "final", "import", "codex", "focused", "gen_ai.assistant.message", map[string]any{
			"gen_ai.message.phase":   "final_answer",
			"gen_ai.output.messages": `[{"role":"assistant","parts":[{"type":"output_text","content":"finalresponse"}]}]`,
		}),
		recorded(t, "progress", "otel", "codex", "focused", "codex.agent_response", map[string]any{"phase": "commentary", "response": "intermediateupdate"}),
		recorded(t, "ambiguous", "otel", "codex", "focused", "codex.agent_response", map[string]any{"response": "answerwithoutphase"}),
		recorded(t, "stats", "otel", "codex", "focused", "codex.sse_event", map[string]any{"event.kind": "response.completed", "output_token_count": "9827415"}),
		recorded(t, "tool", "otel", "codex", "focused", "codex.tool_result", map[string]any{"tool_name": "exec_command", "arguments": `{"cmd":"toolcommand","extra":"toolargument"}`, "output": "toolstdout"}),
		recorded(t, "model", "otel", "codex", "focused", "chat", map[string]any{
			"gen_ai.input.messages":  `[{"role":"system","content":"systemprompt"},{"role":"user","content":"replayedhistory"}]`,
			"gen_ai.output.messages": `[{"role":"assistant","phase":"commentary","content":"arrayprogress"},{"role":"assistant","phase":"final_answer","content":"embeddedsynthesis"}]`,
		}),
		recorded(t, "failed", "otel", "codex", "focused", "network_failure", map[string]any{"error.message": "connectionrefused", "exception.stacktrace": "stacklocation"}),
	)
	for _, query := range []string{"humanrequest", "finalresponse", "answerwithoutphase", "toolcommand", "toolargument", "toolstdout", "embeddedsynthesis", "connectionrefused", "stacklocation"} {
		if rows := searchSessions(t, a, query); len(rows) != 1 || rows[0].ID != "codex:focused" {
			t.Errorf("missing %q: %v", query, rows)
		}
	}
	for _, query := range []string{"injectedcontext", "systemprompt", "replayedhistory", "transportthread", "sdkversion", "intermediateupdate", "arrayprogress", "9827415"} {
		if rows := searchSessions(t, a, query); len(rows) != 0 {
			t.Errorf("unexpected match for %q: %v", query, rows)
		}
	}
	usageWant(t, sessionUsage(t, a, "codex:focused", ""), "output_tokens", "9827415")
}

func TestFullTextSearchUpgradeReplacesOldPostings(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	e := recorded(t, "message", "import", "codex", "legacy", "gen_ai.user.message", map[string]any{
		"prompt": "keptmessage", "thread.name": "legacytransport", "request_id": "unchanged-request-id",
	})
	save(t, a, e)
	if _, err := a.DB.Exec(`DELETE FROM event_search WHERE rowid=(SELECT rowid FROM search_documents WHERE event_id=?1);
	  INSERT INTO event_search(rowid,text) SELECT rowid,'legacytransport' FROM search_documents WHERE event_id=?1;`, e.ID); err != nil {
		t.Fatal(err)
	}
	if rows := searchSessions(t, a, "legacytransport"); len(rows) != 1 {
		t.Fatal("legacy index fixture is missing")
	}
	if _, err := a.DB.Exec("DELETE FROM schema_migrations WHERE version=8"); err != nil {
		t.Fatal(err)
	}
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, a.DB, plan); err != nil {
		t.Fatal(err)
	}
	// Fail after clearing the old postings in the write transaction.
	if _, err = a.DB.Exec("UPDATE records SET raw='invalid' WHERE id=?", e.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.prepareDatabase(ctx); err == nil {
		t.Fatal("rebuild unexpectedly accepted an unreadable record")
	}
	if rows := searchSessions(t, a, "legacytransport"); len(rows) != 1 {
		t.Fatal("failed rebuild published an empty index", rows)
	}
	if _, err = a.DB.Exec("UPDATE records SET raw=? WHERE id=?", []byte(e.Raw), e.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.prepareDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := searchSessions(t, a, "legacytransport"); len(rows) != 0 {
		t.Fatal("legacy postings survived the rebuild", rows)
	}
	if rows := searchSessions(t, a, "keptmessage"); len(rows) != 1 {
		t.Fatal("message was not indexed again", rows)
	}
	if rows := identifierSessions(t, a, "request_id", "unchanged-request-id"); len(rows) != 1 {
		t.Fatal("exact ID stopped matching", rows)
	}
	var raw json.RawMessage
	if err = a.DB.QueryRow("SELECT raw FROM records WHERE id=?", e.ID).Scan(&raw); err != nil || string(raw) != string(e.Raw) {
		t.Fatal("rebuild changed the original record", err)
	}
}
