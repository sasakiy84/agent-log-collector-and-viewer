package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const token = "test-token-at-least-16"

// Only migration tests need to replay the complete schema history. Behavioral
// tests copy an immutable, closed empty database; each still owns its own file.
var emptyTestDatabase = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "agentlog-test-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "empty.db")
	a, err := Open(context.Background(), path, token)
	if err != nil {
		return nil, err
	}
	if err = a.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

func setup(t *testing.T) *App {
	t.Helper()
	data, e := emptyTestDatabase()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "test.db")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	// The copied fixture has no dirty queues. Exercise initialization once when
	// creating it, rather than rerunning all historical jobs for every API test.
	a, e := newApp(context.Background(), token)
	if e != nil {
		t.Fatal(e)
	}
	for _, step := range []func() error{
		func() error { return a.openDatabase(a.ctx, path) },
		func() error { return a.initReaders(path) },
		func() error { return a.initArchives(path) },
		a.startSkillIndexer,
		a.startProcessingIndexer,
	} {
		if e = step(); e != nil {
			a.Close()
			t.Fatal(e)
		}
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// Tests inspect stored originals in small fixtures. Production downloads stream
// rows and do not expose an unbounded materializing read API.
func (a *App) readRecords(ctx context.Context, session, origin string, limit, off int) ([]Event, error) {
	rows, err := a.readers.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE session=? AND (?='' OR origin=?) ORDER BY time,sequence,id LIMIT ? OFFSET ?", session, origin, origin, limit, off)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
func request(a *App, path, body, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func logBody(source, sid, agent, parent string) string {
	return fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":%q}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1780000000000000000","traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","attributes":[{"key":"session.id","value":{"stringValue":%q}},{"key":"agent_id","value":{"stringValue":%q}},{"key":"parent_agent_id","value":{"stringValue":%q}}],"body":{"stringValue":"hello <script>alert(1)</script>"}}]}]}]}`, source, sid, agent, parent)
}
func TestLogsAndAuth(t *testing.T) {
	a := setup(t)
	for _, source := range []string{"codex", "claude", "copilot"} {
		w := request(a, "/v1/logs", logBody(source, "demo", "", ""), token)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", source, w.Code, w.Body.String())
		}
		events, e := a.readRecords(context.Background(), source+":demo", "", 201, 0)
		if e != nil || len(events) != 1 {
			t.Fatalf("%v %#v", e, events)
		}
		if events[0].Trace != "0123456789abcdef0123456789abcdef" || !strings.Contains(events[0].Name, "<script>") {
			t.Fatal(events[0])
		}
	}
	if w := request(a, "/v1/logs", "{}", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	login := request(a, "/api/login", `{"token":"`+token+`"}`, "")
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	for _, path := range []string{"/api/stats", "/api/sessions", "/api/session?id=codex:demo", "/api/export?session=codex:demo"} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/v1/logs", strings.NewReader("{}"))
	r.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("cookie allowed ingestion")
	}
	r = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Agentlog") {
		t.Fatal(w.Code)
	}
}
func TestGzipProtobufDedup(t *testing.T) {
	a := setup(t)
	b, _ := proto.Marshal(&logs.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{Resource: &resource.Resource{Attributes: []*common.KeyValue{{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "codex"}}}}}, ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{Body: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "protobuf"}}}}}}}}})
	var buf bytes.Buffer
	g := gzip.NewWriter(&buf)
	g.Write(b)
	g.Close()
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/v1/logs", bytes.NewReader(buf.Bytes()))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/x-protobuf")
		r.Header.Set("Content-Encoding", "gzip")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var n int
	a.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestRelationshipsAcrossBatches(t *testing.T) {
	a := setup(t)
	for _, args := range [][2]string{{"child", "root"}, {"root", ""}} {
		if w := request(a, "/v1/logs", logBody("claude", "s", args[0], args[1]), token); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	v, _ := a.readRecords(context.Background(), "claude:s/agent:child", "", 200, 0)
	if len(v) != 1 || v[0].Parent != "claude:s/agent:root" {
		t.Fatal(v)
	}
	const child = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"copilot"}}]},"scopeSpans":[{"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spanId":"bbbbbbbbbbbbbbbb","parentSpanId":"cccccccccccccccc","name":"child"}]}]}]}`
	const parent = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"copilot"}}]},"scopeSpans":[{"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spanId":"cccccccccccccccc","name":"root","attributes":[{"key":"gen_ai.conversation.id","value":{"stringValue":"conversation"}}]}]}]}]}`
	for _, b := range []string{child, parent} {
		if w := request(a, "/v1/traces", b, token); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	v, e := a.readRecords(context.Background(), "copilot:conversation", "", 200, 0)
	if e != nil || len(v) != 2 {
		t.Fatal(e, v)
	}
}
func TestMetricsAndValidation(t *testing.T) {
	a := setup(t)
	b := `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex"}}]},"scopeMetrics":[{"metrics":[{"name":"tokens","sum":{"aggregationTemporality":2,"isMonotonic":true,"dataPoints":[{"timeUnixNano":"1780000000000000000","asInt":"10","attributes":[{"key":"conversation.id","value":{"stringValue":"s"}}]},{"timeUnixNano":"1780000001000000000","asInt":"20","attributes":[{"key":"conversation.id","value":{"stringValue":"s"}}]},{"asInt":"30"}]}}]}]}]}`
	if w := request(a, "/v1/metrics", b, token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	v, _ := a.readRecords(context.Background(), "codex:s", "", 200, 0)
	if len(v) != 2 || !strings.Contains(string(v[1].Raw), `"asInt":"20"`) {
		t.Fatal(v)
	}
	v, _ = a.readRecords(context.Background(), "unassigned:codex", "", 200, 0)
	if len(v) != 1 {
		t.Fatal(v)
	}
	for _, b := range []string{"{", `{"resourceLogs":"bad"}`} {
		if w := request(a, "/v1/logs", b, token); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if _, e := Open(context.Background(), ":memory:", "short"); e == nil {
		t.Fatal("short token accepted")
	}
}
func TestPersistencePagination(t *testing.T) {
	p := filepath.Join(t.TempDir(), "persist.db")
	a, e := Open(context.Background(), p, token)
	if e != nil {
		t.Fatal(e)
	}
	v := []Event{}
	for i := 0; i < 205; i++ {
		v = append(v, Event{Session: "s", Time: fmt.Sprintf("%04d", i), Name: "n", Raw: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))})
	}
	if e = a.Save(context.Background(), v); e != nil {
		t.Fatal(e)
	}
	a.Close()
	a, e = Open(context.Background(), p, token)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	v, e = a.readRecords(context.Background(), "s", "", 200, 200)
	if e != nil || len(v) != 5 || v[0].Time != "0200" {
		t.Fatal(e, v)
	}
	a.Close()
	a, e = Open(context.Background(), p, token)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	v, e = a.readRecords(context.Background(), "s", "", 300, 0)
	if e != nil || len(v) != 205 {
		t.Fatal("old records lost after restart", e, len(v))
	}
}

func TestSubsecondOrderingAndAtomicRejection(t *testing.T) {
	a := setup(t)
	b := logBody("codex", "fraction", "", "")
	if w := request(a, "/v1/logs", b, token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	b = strings.Replace(b, "1780000000000000000", "1780000000100000000", 1)
	if w := request(a, "/v1/logs", b, token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	v, _ := a.readRecords(context.Background(), "codex:fraction", "", 200, 0)
	if len(v) != 2 || v[0].Time != "2026-05-28T20:26:40.000000000Z" || v[1].Time != "2026-05-28T20:26:40.100000000Z" {
		t.Fatal(v)
	}
	records := strings.Repeat(`{"body":{"stringValue":"x"}},`, 10000) + `{"body":{"stringValue":"last"}}`
	body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + records + `]}]}]}`
	if w := request(a, "/v1/logs", body, token); w.Code < 400 {
		t.Fatal("oversized batch accepted")
	}
	var n int
	a.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&n)
	if n != 2 {
		t.Fatal("partial batch stored", n)
	}
}

func TestSessionPageMetadataAndSearch(t *testing.T) {
	a := setup(t)
	for _, body := range []string{logBody("claude", "parent", "", ""), logBody("claude", "parent", "child", "")} {
		if w := request(a, "/v1/logs", body, token); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	w := get("/api/session?id=claude:parent")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var m struct {
		Count    int             `json:"count"`
		Children []string        `json:"children"`
		Preview  json.RawMessage `json:"preview"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &m); e != nil {
		t.Fatal(e)
	}
	if m.Count != 1 || len(m.Children) != 1 || m.Children[0] != "claude:parent/agent:child" || len(m.Preview) == 0 {
		t.Fatal(m)
	}
	if w = get("/api/session?id=missing"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = get("/api/sessions?q=alert")
	var sessions []map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &sessions); e != nil || len(sessions) != 1 || sessions[0]["id"] != "claude:parent" || w.Header().Get("X-Total-Count") != "1" {
		t.Fatal(e, w.Body.String())
	}
	w = get("/sessions/claude%3Aparent%2Fagent%3Achild")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "detailpage") {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "/api/session?id=claude:parent", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("metadata exposed")
	}
}

func TestProjectQueryAllSignals(t *testing.T) {
	a := setup(t)
	for _, signal := range []string{"logs", "traces", "metrics"} {
		body := "{}"
		switch signal {
		case "logs":
			body = logBody("codex", "project", "", "")
		case "traces":
			body = `{"resourceSpans":[{"scopeSpans":[{"spans":[{"name":"test"}]}]}]}`
		case "metrics":
			body = `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"test","gauge":{"dataPoints":[{"asInt":"1"}]}}]}]}]}`
		}
		w := request(a, "/v1/"+signal+"?project=shop&tag=backend&tag=go", body, token)
		if w.Code != 200 {
			t.Fatal(signal, w.Body.String())
		}
	}
	rows, e := a.DB.Query("SELECT raw FROM events")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		if !strings.Contains(raw, "service.namespace") || !strings.Contains(raw, "shop") || !strings.Contains(raw, "backend,go") {
			t.Fatal(raw)
		}
		n++
	}
	if n != 3 {
		t.Fatal(n)
	}
}
func TestProjectResourcePrecedence(t *testing.T) {
	root := map[string]any{"resourceLogs": []any{map[string]any{"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.namespace", "value": map[string]any{"stringValue": "explicit"}}}}}}}
	if e := applyProjectQuery(root, "logs", url.Values{"project": []string{"query"}}); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(root)
	if strings.Contains(string(b), `"query"`) {
		t.Fatal(string(b))
	}
	if e := applyProjectQuery(root, "logs", url.Values{"project": []string{"one", "two"}}); e == nil {
		t.Fatal("ambiguous project accepted")
	}
}

func TestStandardAgentSessionScope(t *testing.T) {
	for _, operation := range []string{"execute_tool", "invoke_agent"} {
		t.Run(operation, func(t *testing.T) {
			a := setup(t)
			body := strings.Replace(logBody("claude", "s", "child", ""), `"key":"agent_id"`, `"key":"gen_ai.agent.id"`, 1)
			var root map[string]any
			if err := json.Unmarshal([]byte(body), &root); err != nil {
				t.Fatal(err)
			}
			resource := obj(arr(root["resourceLogs"])[0])
			scope := obj(arr(resource["scopeLogs"])[0])
			record := obj(arr(scope["logRecords"])[0])
			record["attributes"] = append(arr(record["attributes"]), map[string]any{"key": "gen_ai.operation.name", "value": map[string]any{"stringValue": operation}})
			encoded, _ := json.Marshal(root)
			if w := request(a, "/v1/logs", string(encoded), token); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			want := "claude:s/agent:child"
			if operation == "invoke_agent" {
				want = "claude:s"
			}
			events, err := a.readRecords(context.Background(), want, "", 10, 0)
			if err != nil || len(events) != 1 {
				t.Fatal(want, events, err)
			}
		})
	}
}

func TestDatabaseFailureLogsCauseWithoutCredentials(t *testing.T) {
	a := setup(t)
	a.Close()
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	req := httptest.NewRequest("GET", "/api/sessions?q=private-query-value", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, req)
	if response.Code != 500 || response.Body.String() != "Database error\n" {
		t.Fatal(response.Code, response.Body.String())
	}
	output := logs.String()
	if !strings.Contains(output, "GET /api/sessions") || !strings.Contains(output, "database is closed") {
		t.Fatalf("underlying error was not logged: %s", output)
	}
	if strings.Contains(output, token) || strings.Contains(output, "private-query-value") {
		t.Fatal("credentials or query leaked to error log")
	}
}
