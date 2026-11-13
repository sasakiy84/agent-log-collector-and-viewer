package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const token = "test-token-at-least-16"

func setup(t *testing.T) *App {
	t.Helper()
	a, e := Open(filepath.Join(t.TempDir(), "test.db"), token, 30)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.DB.Close() })
	return a
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
		events, e := a.readEvents(source+":demo", 201, 0)
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
	for _, path := range []string{"/api/stats", "/api/sessions", "/api/events?session=codex:demo", "/api/export?session=codex:demo"} {
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
	v, _ := a.readEvents("claude:s/agent:child", 200, 0)
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
	v, e := a.readEvents("copilot:conversation", 200, 0)
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
	v, _ := a.readEvents("codex:s", 200, 0)
	if len(v) != 2 || !strings.Contains(string(v[1].Raw), `"asInt":"20"`) {
		t.Fatal(v)
	}
	v, _ = a.readEvents("unassigned:codex", 200, 0)
	if len(v) != 1 {
		t.Fatal(v)
	}
	for _, b := range []string{"{", `{"resourceLogs":"bad"}`} {
		if w := request(a, "/v1/logs", b, token); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if _, e := Open(":memory:", "short", 30); e == nil {
		t.Fatal("short token accepted")
	}
}
func TestPersistencePaginationRetention(t *testing.T) {
	p := filepath.Join(t.TempDir(), "persist.db")
	a, e := Open(p, token, 30)
	if e != nil {
		t.Fatal(e)
	}
	v := []Event{}
	for i := 0; i < 205; i++ {
		v = append(v, Event{Session: "s", Time: fmt.Sprintf("%04d", i), Name: "n", Raw: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))})
	}
	if e = a.Save(v); e != nil {
		t.Fatal(e)
	}
	a.DB.Close()
	a, e = Open(p, token, 30)
	if e != nil {
		t.Fatal(e)
	}
	defer a.DB.Close()
	v, e = a.readEvents("s", 200, 200)
	if e != nil || len(v) != 5 || v[0].Time != "0200" {
		t.Fatal(e, v)
	}
	a.DB.Exec("UPDATE events SET received=?", time.Now().Add(-31*24*time.Hour).Unix())
	if e = a.Prune(); e != nil {
		t.Fatal(e)
	}
	v, _ = a.readEvents("s", 200, 0)
	if len(v) != 0 {
		t.Fatal("pruning failed")
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
	v, _ := a.readEvents("codex:fraction", 200, 0)
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
