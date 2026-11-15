package importer_test

import (
	"context"
	"encoding/json"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/app"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const token = "import-test-token-at-least-16"

func file(t *testing.T, dir, name, data string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func setup(t *testing.T) (*app.App, *httptest.Server) {
	t.Helper()
	a, e := app.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), token)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(a.Handler())
	t.Cleanup(func() { s.Close(); a.Close() })
	return a, s
}
func TestThreeFormatsAndReplay(t *testing.T) {
	db, server := setup(t)
	dir := t.TempDir()
	file(t, dir, "codex.jsonl", `{"type":"session_meta","timestamp":"2026-11-01T00:00:00Z","payload":{"id":"cx","cwd":"/work/shop"}}
{"type":"event_msg","timestamp":"2026-11-01T00:00:01Z","payload":{"type":"user_message","message":"hello"}}
{"type":"response_item","timestamp":"2026-11-01T00:00:01Z","payload":{"type":"message","role":"user","id":"u1","content":[{"type":"input_text","text":"hello"}]}}
{"type":"response_item","timestamp":"2026-11-01T00:00:02Z","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./...\"}"}}
{"type":"response_item","timestamp":"2026-11-01T00:00:03Z","payload":{"type":"function_call_output","call_id":"c1","output":"PASS"}}
{"type":"response_item","timestamp":"2026-11-01T00:00:04Z","payload":{"type":"message","role":"assistant","id":"a1","content":[{"type":"output_text","text":"Done"}]}}
`)
	file(t, dir, "claude.jsonl", `{"type":"user","uuid":"cu","sessionId":"cl","timestamp":"2026-11-01T00:01:00Z","cwd":"/work/shop","message":{"content":"Review this"}}
{"type":"assistant","uuid":"ca","sessionId":"cl","timestamp":"2026-11-01T00:01:01Z","message":{"model":"claude-model","content":[{"type":"text","text":"Checking"},{"type":"tool_use","id":"ct","name":"Read","input":{"file_path":"a.go"}}],"usage":{"input_tokens":42,"output_tokens":9}}}
{"type":"user","uuid":"cr","sessionId":"cl","timestamp":"2026-11-01T00:01:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"ct","content":[{"type":"text","text":"ENOENT"}],"is_error":true}]}}
`)
	file(t, dir, "cp/events.jsonl", `{"type":"session.start","id":"s0","timestamp":"2026-11-01T00:02:00Z","data":{"sessionId":"cp","context":{"cwd":"/work/shop"}}}
{"type":"user.message","id":"s1","timestamp":"2026-11-01T00:02:01Z","data":{"content":"Test it"}}
{"type":"tool.execution_start","id":"s2","timestamp":"2026-11-01T00:02:02Z","data":{"toolCallId":"st","toolName":"shell","arguments":{"command":"go test"}}}
{"type":"tool.execution_complete","id":"s3","timestamp":"2026-11-01T00:02:03Z","data":{"toolCallId":"st","success":true,"result":{"content":"PASS"}}}
{"type":"assistant.message","id":"s4","timestamp":"2026-11-01T00:02:04Z","data":{"content":"All tests pass"}}
`)
	o := importer.Options{Dirs: []string{dir}, Endpoint: server.URL, Token: token, Project: "shop", Tags: []string{"backend", "go"}}
	stats, e := importer.Run(context.Background(), o, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if stats.Records != 13 || stats.Files != 3 {
		t.Fatal(stats)
	}
	var count, sessions int
	if e = db.DB.QueryRow("SELECT COUNT(*),COUNT(DISTINCT session) FROM events").Scan(&count, &sessions); e != nil {
		t.Fatal(e)
	}
	if count != 13 || sessions != 3 {
		t.Fatal(count, sessions)
	}
	_, e = importer.Run(context.Background(), o, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	db.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if count != 13 {
		t.Fatal("duplicate import", count)
	}
	// Re-labeling updates imported metadata rather than duplicating the timeline.
	o.Project = "renamed"
	_, e = importer.Run(context.Background(), o, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	var raw string
	db.DB.QueryRow("SELECT raw FROM events WHERE session='codex:cx' LIMIT 1").Scan(&raw)
	if !strings.Contains(raw, "renamed") || !strings.Contains(raw, "project.tags") {
		t.Fatal(raw)
	}
	db.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if count != 13 {
		t.Fatal(count)
	}
	var toolRaw string
	db.DB.QueryRow("SELECT raw FROM events WHERE session='claude:cl' AND CAST(raw AS TEXT) LIKE '%ENOENT%' LIMIT 1").Scan(&toolRaw)
	if !strings.Contains(toolRaw, `"stringValue":"Read"`) || !strings.Contains(toolRaw, `"boolValue":false`) {
		t.Fatal(toolRaw)
	}
}
func TestJournalAndSubagent(t *testing.T) {
	db, server := setup(t)
	dir := t.TempDir()
	file(t, dir, "cp/events.1.jsonl", `{"type":"session.start","timestamp":"2026-11-01T00:00:00Z","data":{"sessionId":"journal"}}
{"type":"user.message","id":"original","timestamp":"2026-11-01T00:00:01Z","data":{"content":"one"}}
`)
	file(t, dir, "cp/events.jsonl", `{"type":"session.journal_link","data":{"sessionId":"journal","previousFile":"events.1.jsonl","version":1}}
{"type":"user.message","id":"checkpoint","checkpoint":true,"data":{"content":"one"}}
{"type":"assistant.message","id":"new","timestamp":"2026-11-01T00:00:02Z","data":{"content":"two"}}
`)
	file(t, dir, "cl/subagents/agent-review.jsonl", `{"type":"assistant","sessionId":"root","uuid":"child","timestamp":"2026-11-01T00:00:00Z","message":{"content":[{"type":"text","text":"review"}]}}
`)
	stats, e := importer.Run(context.Background(), importer.Options{Dirs: []string{dir}, Endpoint: server.URL, Token: token}, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if stats.Records != 3 {
		t.Fatal(stats)
	}
	var parent string
	if e = db.DB.QueryRow("SELECT parent FROM events WHERE session='claude:root/agent:review'").Scan(&parent); e != nil || parent != "claude:root" {
		t.Fatal(e, parent)
	}
}
func TestDryRunFailuresAndBounds(t *testing.T) {
	dir := t.TempDir()
	p := file(t, dir, "one.jsonl", `{"type":"user","sessionId":"one","timestamp":"2026-11-01T00:00:00Z","message":{"content":"ok"}}
{"incomplete":`)
	stats, e := importer.Run(context.Background(), importer.Options{Files: []string{p}, DryRun: true}, io.Discard)
	if e != nil || stats.Records != 1 || stats.Skipped != 1 {
		t.Fatal(stats, e)
	}
	bad := file(t, dir, "bad.jsonl", "not-json\n{}")
	_, e = importer.Run(context.Background(), importer.Options{Files: []string{bad}, DryRun: true}, io.Discard)
	if e == nil {
		t.Fatal("invalid interior line accepted")
	}
	linked := file(t, dir, "linked.jsonl", `{"type":"session_meta","payload":{"id":"x","history_base":{"rollout_id":"other"}}}`)
	_, e = importer.Run(context.Background(), importer.Options{Files: []string{linked}, DryRun: true}, io.Discard)
	if e == nil {
		t.Fatal("unsupported linked history silently imported")
	}
	// Missing predecessor archives and traversals are rejected.
	linked = file(t, dir, "events.jsonl", `{"type":"session.journal_link","data":{"sessionId":"x","previousFile":"../secret"}}`)
	_, e = importer.Run(context.Background(), importer.Options{Files: []string{linked}, DryRun: true}, io.Discard)
	if e == nil {
		t.Fatal("journal traversal accepted")
	}
}
func TestBatchLimitsAndEndpointQuery(t *testing.T) {
	dir := t.TempDir()
	var records strings.Builder
	for i := 0; i < 450; i++ {
		b, _ := json.Marshal(map[string]any{"type": "user", "uuid": i, "sessionId": "batch", "timestamp": "2026-11-01T00:00:00Z", "message": map[string]any{"content": "text"}})
		records.Write(b)
		records.WriteByte('\n')
	}
	p := file(t, dir, "batch.jsonl", records.String())
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/logs" || r.URL.Query().Get("project") != "x" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error(r.URL, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		if len(b) > 16<<20 {
			t.Error("batch too large")
		}
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	stats, e := importer.Run(context.Background(), importer.Options{Files: []string{p}, Endpoint: server.URL + "?project=x", Token: token}, io.Discard)
	if e != nil || calls != 3 || stats.Records != 450 {
		t.Fatal(stats, calls, e)
	}
}

func TestWorkingDirectoryAndAttributeTypes(t *testing.T) {
	db, server := setup(t)
	dir := t.TempDir()
	file(t, dir, "other.jsonl", `{"type":"user","sessionId":"other","cwd":"/work/shopper","uuid":"o","message":{"content":"other"}}`)
	file(t, dir, "shop.jsonl", `{"type":"user","sessionId":"shop","cwd":"/work/shop/src","uuid":"u","message":{"content":"included"}}
{"type":"assistant","sessionId":"shop","uuid":"a","message":{"content":[{"type":"text","text":"done"}],"usage":{"input_tokens":9007199254740993,"output_tokens":0}}}
`)
	stats, e := importer.Run(context.Background(), importer.Options{Dirs: []string{dir}, WorkingDir: "/work/shop", Endpoint: server.URL, Token: token}, io.Discard)
	if e != nil || stats.Records != 3 {
		t.Fatal(stats, e)
	}
	var raw string
	if e = db.DB.QueryRow("SELECT raw FROM events WHERE CAST(raw AS TEXT) LIKE '%9007199254740993%'").Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(raw, `"intValue":"9007199254740993"`) || !strings.Contains(raw, `"intValue":"0"`) {
		t.Fatal("usage lost integer precision", raw)
	}
	var count int
	db.DB.QueryRow("SELECT COUNT(*) FROM events WHERE session='claude:other'").Scan(&count)
	if count != 0 {
		t.Fatal("cwd prefix matched sibling")
	}
}
func TestImporterDoesNotForwardTokenOnRedirect(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte("{}")) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	dir := t.TempDir()
	p := file(t, dir, "x.jsonl", `{"type":"user","sessionId":"x","uuid":"u","message":{"content":"x"}}`)
	_, e := importer.Run(context.Background(), importer.Options{Files: []string{p}, Endpoint: redirect.URL, Token: token}, io.Discard)
	if e == nil || called {
		t.Fatal("redirect followed or accepted", e)
	}
}

func TestRepeatedEventIDUsesLatestContent(t *testing.T) {
	db, server := setup(t)
	dir := t.TempDir()
	p := file(t, dir, "s.jsonl", `{"type":"assistant","uuid":"same","sessionId":"s","timestamp":"2026-11-01T00:00:00Z","message":{"content":"partial"}}
{"type":"assistant","uuid":"same","sessionId":"s","timestamp":"2026-11-01T00:00:01Z","message":{"content":"complete"}}
`)
	_, e := importer.Run(context.Background(), importer.Options{Files: []string{p}, Token: token, Endpoint: server.URL}, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	var n int
	var raw string
	db.DB.QueryRow("SELECT COUNT(*),CAST(raw AS TEXT) FROM events").Scan(&n, &raw)
	if n != 1 || !strings.Contains(raw, "complete") {
		t.Fatal(n, raw)
	}
}
