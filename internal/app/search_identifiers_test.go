package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func identifierSessions(t *testing.T, a *App, key, value string) []SessionSummary {
	t.Helper()
	var rows []SessionSummary
	getJSON(t, a, "/api/sessions?"+url.Values{"id_key": {key}, "id_value": {value}}.Encode(), &rows)
	return rows
}

func TestSearchIndexesStringValuesAndExactIdentifiersSeparately(t *testing.T) {
	a := setup(t)
	e := recorded(t, "event-private-id", "import", "codex", "session-private-id", "user_prompt", map[string]any{
		"prompt": "日本語の認証を調査", "gen_ai.tool.call.id": "Call-識別子専用-Ａ", "gen_ai.message.id": "Message-Case",
		"counter": 987654321, "success": true, "build_uuid": "Build-UUID-Value",
		"nested": `{"request_id":"Nested-ID","count":765432198,"enabled":false,"説明":"検索の診断結果","detail":{"count":"概数","enabled":"有効化"}}`,
	})
	e.Trace, e.Span = "Trace-Case", "Span-Case"
	doc, _ := decodeDocument(e.Raw)
	doc.Record["timeUnixNano"] = "1794328119866000000"
	doc.Record["asInt"] = "654321987"
	doc.Record["count"] = "543219876"
	doc.Record["positive"] = map[string]any{"bucketCounts": []any{"432198765"}}
	doc.Record["attributes"] = append(arr(doc.Record["attributes"]), map[string]any{"key": "large_counter", "value": map[string]any{"intValue": "9007199254740993"}})
	e.Raw, _ = json.Marshal(doc)
	save(t, a, e)
	for _, query := range []string{"認証", "診断", "調査する", "概数", "有効化"} {
		if len(searchSessions(t, a, query)) != 1 {
			t.Fatal("string value not indexed", query)
		}
	}
	for _, query := range []string{"987654321", "765432198", "9007199254740993", "1794328119866000000", "654321987", "543219876", "432198765", "true", "false", "識別子専用", "Nested-ID", "Trace-Case", "session-private-id", "Build-UUID-Value", "counter", "説明", "stringValue"} {
		if len(searchSessions(t, a, query)) != 0 {
			t.Fatal("non-content value entered full-text index", query)
		}
	}
	for key, value := range map[string]string{"session.id": e.Session, "event.id": e.ID, "trace.id": e.Trace, "span.id": e.Span, "call.id": "Call-識別子専用-Ａ", "gen_ai.tool.call.id": "Call-識別子専用-Ａ", "message.id": "Message-Case", "request_id": "Nested-ID", "build_uuid": "Build-UUID-Value", "": "Message-Case"} {
		if rows := identifierSessions(t, a, key, value); len(rows) != 1 || rows[0].ID != e.Session {
			t.Fatal("exact ID lookup failed", key, value, rows)
		}
	}
	for _, value := range []string{"call-識別子専用-Ａ", "Call-識別子専用-A", "Call-識別子専用", "識別子専用"} {
		if len(identifierSessions(t, a, "call.id", value)) != 0 {
			t.Fatal("ID was normalized or partially matched", value)
		}
	}
	for _, params := range []url.Values{{"id_key": {"prompt"}, "id_value": {"認証"}}, {"id_key": {"event.id"}}, {"id_value": {strings.Repeat("x", 4097)}}} {
		if response := archiveReq(a, "GET", "/api/sessions?"+params.Encode(), ""); response.Code != 400 {
			t.Fatal("invalid ID filter accepted", params, response.Code)
		}
	}
}

func TestIdentifierFilterUpdatesMovesDeletesAndCombinesWithText(t *testing.T) {
	a := setup(t)
	e := recorded(t, "changing", "import", "codex", "before", "user_prompt", map[string]any{"prompt": "認証を修正", "call_id": "old-id", "cwd": "/work/a"})
	save(t, a, e)
	var folders folderPage
	getJSON(t, a, "/api/folders?id_key=call.id&id_value=missing", &folders)
	if folders.Total != 0 {
		t.Fatal("ID filter incorrectly used unfiltered folder cache", folders)
	}
	for query, want := range map[string]int{"認証": 1, "未登録": 0} {
		var sessions []SessionSummary
		getJSON(t, a, "/api/sessions?"+url.Values{"id_key": {"call.id"}, "id_value": {"old-id"}, "q": {query}}.Encode(), &sessions)
		if len(sessions) != want {
			t.Fatal("text and ID filters were not combined", query, sessions)
		}
	}
	e = recorded(t, "changing", "import", "codex", "after", "user_prompt", map[string]any{"prompt": "接続を修正", "call_id": "new-id"})
	save(t, a, e)
	if len(identifierSessions(t, a, "call.id", "old-id")) != 0 {
		t.Fatal("old identifier survived replacement")
	}
	if rows := identifierSessions(t, a, "call.id", "new-id"); len(rows) != 1 || rows[0].ID != "codex:after" {
		t.Fatal("identifier did not follow moved event", rows)
	}
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM events WHERE id=?", e.ID); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	if len(identifierSessions(t, a, "call.id", "new-id")) != 0 {
		t.Fatal("identifier survived event deletion")
	}
}

func TestStringSearchUpgradeRemovesOldNumericAndIDPostingsOnce(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t, filepath.Join(t.TempDir(), "old-search.db"))
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan[:12]); err != nil {
		t.Fatal(err)
	}
	e := recorded(t, "old", "import", "codex", "old", "user_prompt", map[string]any{"prompt": "認証の確認", "request_id": "Old-Request", "counter": 987654321})
	if _, err = db.Exec("INSERT INTO events("+eventColumns+",kind,tool_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'user','')", e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, e.Raw, e.Sequence, "import"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO search_documents(rowid,event_id,session) VALUES(1,?,?)", e.ID, e.Session); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO event_search(rowid,text) VALUES(1,'987654321 old request')"); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	a := &App{DB: db}
	if err = a.runMaintenance(ctx, "search_strings.v1", backfillSearchStrings, 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM event_search WHERE event_search MATCH '987654321 OR request'").Scan(&count); err != nil || count != 0 {
		t.Fatal("old full-text terms survived rebuild", count, err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM search_identifiers WHERE key='request_id' AND value='Old-Request'").Scan(&count); err != nil || count != 1 {
		t.Fatal("existing ID not indexed", count, err)
	}
	if err = a.runMaintenance(ctx, "search_strings.v1", func(context.Context, *sql.Tx, string) (string, int, error) {
		t.Fatal("completed rebuild repeated on restart")
		return "", 0, nil
	}, 0); err != nil {
		t.Fatal(err)
	}
}
