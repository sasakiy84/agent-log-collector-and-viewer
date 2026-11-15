package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestInternalCodexSessionsRequireExplicitTemporaryAndEphemeralMetadata(t *testing.T) {
	a := setup(t)
	tests := []struct {
		name, source string
		attrs        map[string]any
		hidden       bool
	}{
		{"automatic", "codex", map[string]any{"session_init.ephemeral": true, "rpc.request_id": "temporary-structured-test"}, true},
		{"automatic-turn", "codex", map[string]any{"session_init.ephemeral": "true", "rpc.request_id": "temporary-structured-turn-test"}, true},
		{"user-ephemeral", "codex", map[string]any{"session_init.ephemeral": true}, false},
		{"rpc-only", "codex", map[string]any{"rpc.request_id": "temporary-structured-test"}, false},
		{"persistent", "codex", map[string]any{"session_init.ephemeral": false, "rpc.request_id": "temporary-structured-test"}, false},
		{"ordinary-rpc", "codex", map[string]any{"session_init.ephemeral": true, "rpc.request_id": "user-request"}, false},
		{"title-prompt", "codex", map[string]any{"model": "gpt-5.6-luna", "prompt": "Generate a concise, single-line task title"}, false},
		{"other-agent", "claude", map[string]any{"session_init.ephemeral": true, "rpc.request_id": "temporary-structured-test"}, false},
	}
	expected := map[string]bool{}
	for _, test := range tests {
		e := recorded(t, test.name, "otel", test.source, test.name, "thread/start", test.attrs)
		save(t, a, e)
		expected[e.Session] = !test.hidden
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	visible := 0
	for _, want := range expected {
		if want {
			visible++
		}
	}
	if len(sessions) != visible {
		t.Fatalf("got %d visible sessions, want %d", len(sessions), visible)
	}
	for _, session := range sessions {
		if !expected[session.ID] {
			t.Fatal("internal session visible", session.ID)
		}
	}
	getJSON(t, a, "/api/sessions?diagnostics=1", &sessions)
	if len(sessions) != len(tests) {
		t.Fatal("internal sessions inaccessible", len(sessions))
	}
	var detail SessionPage
	getJSON(t, a, "/api/session?id=codex:automatic&view=diagnostics", &detail)
	var originals []Event
	getJSON(t, a, "/api/export?session=codex:automatic", &originals)
	if detail.Count != 1 || len(originals) != 1 {
		t.Fatal("hiding modified the underlying session", detail.Count, len(originals))
	}
}

func TestInternalSessionClassificationTracksLateChangedAndRemovedEvidence(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	// Nest metadata in span events, as it can be emitted away from the root.
	init := recorded(t, "init", "otel", "codex", "auto", "session_init.thread_persistence", map[string]any{"session_init.ephemeral": true})
	doc, _ := decodeDocument(init.Raw)
	doc.Record["events"] = []any{map[string]any{"name": "init", "attributes": doc.Record["attributes"]}}
	delete(doc.Record, "attributes")
	init.Raw, _ = json.Marshal(doc)
	rpc := recorded(t, "rpc", "import", "codex", "auto", "thread/start", map[string]any{"rpc.request_id": "temporary-structured-test"})
	prompt := recorded(t, "prompt", "otel", "codex", "auto", "user_prompt", map[string]any{"cwd": "/work/automatic", "prompt": "Synthetic title generation"})
	check := func(visible bool) {
		t.Helper()
		want := 0
		if visible {
			want = 1
		}
		var sessions []SessionSummary
		getJSON(t, a, "/api/sessions", &sessions)
		if len(sessions) != want {
			t.Fatalf("visibility: got %d want %d", len(sessions), want)
		}
		r := httptest.NewRequest("GET", "/api/sessions?offset=200", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("X-Total-Count") != fmt.Sprint(want) {
			t.Fatal("pagination count includes hidden session", w.Code, w.Header())
		}
		var folders folderPage
		getJSON(t, a, "/api/folders", &folders)
		if folders.Total != want {
			t.Fatal("folder cache includes hidden session", folders.Total, want)
		}
		var projects struct{ Sessions int }
		getJSON(t, a, "/api/projects", &projects)
		if projects.Sessions != want {
			t.Fatal("project cache includes hidden session", projects.Sessions, want)
		}
		getJSON(t, a, "/api/projects?diagnostics=1", &projects)
		if projects.Sessions != 1 {
			t.Fatal("project toggle lost hidden session", projects.Sessions)
		}
	}
	save(t, a, prompt, rpc)
	check(true)
	save(t, a, init) // A late marker hides the entire existing session.
	check(false)
	persistent := recorded(t, "persistent", "import", "codex", "auto", "session_init.state_db", map[string]any{"session_init.ephemeral": false})
	save(t, a, persistent) // Contradictory evidence must err toward visibility.
	check(true)
	persistent = recorded(t, "persistent", "import", "codex", "auto", "session_init.state_db", map[string]any{"session_init.ephemeral": true})
	save(t, a, persistent)
	check(false)
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id='import:rpc'"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	check(true) // Cascading evidence deletion must undo the classification.
	save(t, a, rpc)
	check(false)
	rpc = recorded(t, "rpc", "import", "codex", "auto", "thread/start", nil)
	save(t, a, rpc) // Replacing an event without a marker also undoes it.
	check(true)
}

func TestInternalSessionVisibilityFollowsReconciledTraceOwner(t *testing.T) {
	a := setup(t)
	marker := modelSpan(t, "marker", "unused", "child", "root", "thread/start", map[string]any{"session_init.ephemeral": true, "rpc.request_id": "temporary-structured-test"})
	marker.Session = "trace:codex:explicit-trace"
	save(t, a, marker)
	owner := modelSpan(t, "owner", "automatic", "root", "", "user_prompt", map[string]any{"prompt": "Synthetic title generation"})
	save(t, a, owner)
	var session string
	if err := a.DB.QueryRow("SELECT session FROM codex_session_facts WHERE event_id='marker'").Scan(&session); err != nil || session != owner.Session {
		t.Fatal("visibility evidence did not follow trace ownership", session, err)
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 0 {
		t.Fatal("reconciled automatic session visible by default", sessions)
	}
	getJSON(t, a, "/api/sessions?diagnostics=1", &sessions)
	if len(sessions) != 1 || sessions[0].ID != owner.Session {
		t.Fatal("trace ownership split the session", sessions)
	}
}

func TestCodexVisibilityUpgradeBackfillsOnceOutsideSchemaMigration(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t, filepath.Join(t.TempDir(), "before-visibility.db"))
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan[:9]); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 203; i++ {
		attrs := map[string]any{}
		if i == 0 {
			attrs["session_init.ephemeral"] = true
		}
		if i == 202 {
			attrs["rpc.request_id"] = "temporary-structured-test"
		}
		e := recorded(t, fmt.Sprintf("%03d", i), "otel", "codex", "existing", "thread/start", attrs)
		if _, err = db.Exec(`INSERT INTO events(`+eventColumns+`,kind,tool_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'','')`, e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, e.Raw, e.Sequence, "otel"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO session_catalog(session,source,parent,count,start,end,import_count,import_start,import_end,otel_count,otel_start,otel_end,preview)
 VALUES('codex:existing','codex','',203,'','',0,'','',203,'','','{}')`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	var facts, internal int
	if err = db.QueryRow("SELECT COUNT(*) FROM codex_session_facts").Scan(&facts); err != nil || facts != 0 {
		t.Fatal("schema migration scanned historical payloads", facts, err)
	}
	a := &App{DB: db}
	if err = a.runMaintenance(ctx, codexVisibilityJob, backfillSessionVisibility, 0); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT internal FROM session_catalog WHERE session='codex:existing'").Scan(&internal); err != nil || internal != 1 {
		t.Fatal("existing automatic session not classified", internal, err)
	}
	if err = a.runMaintenance(ctx, codexVisibilityJob, func(context.Context, *sql.Tx, string) (string, int, error) {
		t.Fatal("completed visibility job repeated on restart")
		return "", 0, nil
	}, 0); err != nil {
		t.Fatal(err)
	}
}
