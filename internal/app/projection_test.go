package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func recorded(t *testing.T, id, origin, source, session, kind string, at map[string]any) Event {
	t.Helper()
	attributes := []any{}
	for k, v := range at {
		var value map[string]any
		switch v := v.(type) {
		case bool:
			value = map[string]any{"boolValue": v}
		case int:
			value = map[string]any{"intValue": fmt.Sprint(v)}
		default:
			value = map[string]any{"stringValue": v}
		}
		attributes = append(attributes, map[string]any{"key": k, "value": value})
	}
	attributes = append(attributes, map[string]any{"key": "event.name", "value": map[string]any{"stringValue": kind}})
	if origin == "import" {
		attributes = append(attributes, map[string]any{"key": "agentlog.origin", "value": map[string]any{"stringValue": "import"}})
		id = "import:" + id
	}
	raw, err := json.Marshal(map[string]any{"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": source}}}}, "record": map[string]any{"eventName": kind, "attributes": attributes}})
	if err != nil {
		t.Fatal(err)
	}
	return Event{ID: id, Session: source + ":" + session, Source: source, Name: kind, Signal: "logs", Time: "2026-11-14T00:00:00.000000000Z", Raw: raw}
}
func save(t *testing.T, a *App, events ...Event) {
	t.Helper()
	if err := a.Save(events); err != nil {
		t.Fatal(err)
	}
}
func getJSON(t *testing.T, a *App, path string, out any) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
}
func page(t *testing.T, a *App, id, origin string, offset int) SessionPage {
	t.Helper()
	var p SessionPage
	getJSON(t, a, fmt.Sprintf("/api/session?id=%s&origin=%s&offset=%d", url.QueryEscape(id), origin, offset), &p)
	return p
}
func TestToolMergeAndOriginIsolation(t *testing.T) {
	a := setup(t)
	start := recorded(t, "s", "import", "codex", "s", "gen_ai.tool.start", map[string]any{"gen_ai.tool.call.id": "call", "gen_ai.tool.name": "exec", "gen_ai.tool.call.arguments": `{"cmd":"ls"}`})
	result := recorded(t, "r", "import", "codex", "s", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "call", "gen_ai.tool.name": "exec", "gen_ai.tool.call.result": "README.md", "service.namespace": "shop"})
	native := recorded(t, "o", "otel", "codex", "s", "codex.tool_result", map[string]any{"call_id": "call", "tool_name": "exec", "duration_ms": 0, "success": false, "error": "native failure"})
	save(t, a, start, result, native)
	p := page(t, a, "codex:s", "", 0)
	if p.Count != 2 || len(p.Events) != 2 {
		t.Fatal(p)
	}
	var merged TimelineEvent
	for _, e := range p.Events {
		if len(e.Records) == 2 {
			merged = e
		}
	}
	if merged.ID != "import:r" || !hasBlock(merged.Detail, "Output", "README.md") || !hasField(merged.Detail, "Duration", "0 ms") || !merged.Detail.Error || merged.Detail.Project != "shop" {
		t.Fatal(merged)
	}
	imported := page(t, a, "codex:s", "import", 0)
	if imported.Count != 2 {
		t.Fatal(imported)
	}
	for _, e := range imported.Events {
		if e.Detail.Error || hasField(e.Detail, "Duration", "0 ms") || len(e.Records) != 1 {
			t.Fatal("OTEL details leaked", e)
		}
	}
	otel := page(t, a, "codex:s", "otel", 0)
	if otel.Count != 1 || !otel.Events[0].Detail.Error || hasBlock(otel.Events[0].Detail, "Output", "README.md") {
		t.Fatal(otel)
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?q=native%20failure", &sessions)
	if len(sessions) != 1 || sessions[0].Count != 2 {
		t.Fatal(sessions)
	}
	var originals []Event
	getJSON(t, a, "/api/export?session=codex:s", &originals)
	if len(originals) != 3 {
		t.Fatal(originals)
	}
	for _, e := range originals {
		if e.ID == result.ID && !reflect.DeepEqual(e.Raw, result.Raw) {
			t.Fatal("original was modified")
		}
	}
	getJSON(t, a, "/api/export?session=codex:s&origin=otel", &originals)
	if len(originals) != 1 || originals[0].Origin != "otel" {
		t.Fatal(originals)
	}
}
func TestMessagesAcrossAgentsAndArrivalOrders(t *testing.T) {
	for _, source := range []string{"codex", "claude", "copilot"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprint(source, reverse), func(t *testing.T) {
				a := setup(t)
				i := recorded(t, "i", "import", source, "s", "gen_ai.user.message", map[string]any{"event.id": "uuid", "gen_ai.message.id": "message", "prompt": "original"})
				o := recorded(t, "o", "otel", source, "s", "gen_ai.user.message", map[string]any{"message_id": "message", "prompt": "native"})
				if reverse {
					save(t, a, o)
					save(t, a, i)
				} else {
					save(t, a, i)
					save(t, a, o)
				}
				p := page(t, a, source+":s", "", 0)
				if p.Count != 1 || len(p.Events[0].Records) != 2 || len(p.Events[0].Detail.Conflicts) == 0 {
					t.Fatal(p)
				}
				save(t, a, o, i)
				if p = page(t, a, source+":s", "", 0); p.Count != 1 {
					t.Fatal("replay", p)
				}
			})
		}
	}
}
func TestConservativeMatching(t *testing.T) {
	cases := []struct {
		name, ik, ok string
		ia, oa       map[string]any
		signal       string
		want         int
	}{
		{"no id", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"prompt": "same"}, map[string]any{"prompt": "same"}, "", 2},
		{"different namespaces", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same"}, map[string]any{"message_id": "same"}, "", 2},
		{"response id is not message id", "gen_ai.assistant.message", "gen_ai.assistant.message", map[string]any{"gen_ai.message.id": "same"}, map[string]any{"gen_ai.response.id": "same"}, "", 2},
		{"chat context", "gen_ai.user.message", "chat", map[string]any{"gen_ai.message.id": "same"}, map[string]any{"gen_ai.message.id": "same", "gen_ai.operation.name": "chat", "gen_ai.input.messages": `[{"role":"user","parts":[{"content":"same"}]}]`}, "", 2},
		{"metrics", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same"}, map[string]any{"event.id": "same"}, "metrics", 2},
		{"agent scope", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "agent_id": "a"}, map[string]any{"event.id": "same", "agent_id": "b"}, "", 2},
		{"standard agent scope", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "gen_ai.agent.id": "a"}, map[string]any{"event.id": "same", "gen_ai.agent.id": "b"}, "", 2},
		{"agent aliases", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "agent_id": "a"}, map[string]any{"event.id": "same", "gen_ai.agent.id": "a"}, "", 1},
		{"conflicting agent aliases", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "agent_id": "a", "gen_ai.agent.id": "b"}, map[string]any{"event.id": "same", "agent_id": "a"}, "", 2},
		{"invocation target is not actor", "gen_ai.tool.result", "tool_result", map[string]any{"call_id": "same", "gen_ai.operation.name": "invoke_agent"}, map[string]any{"call_id": "same"}, "", 2},
		{"phases", "gen_ai.tool.start", "tool_result", map[string]any{"gen_ai.tool.call.id": "same"}, map[string]any{"call_id": "same"}, "", 2},
		{"different tool names", "gen_ai.tool.result", "tool_result", map[string]any{"gen_ai.tool.call.id": "same", "tool_name": "A"}, map[string]any{"call_id": "same", "tool_name": "B"}, "", 2},
		{"conflicting message IDs", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "gen_ai.message.id": "m1"}, map[string]any{"event_id": "same", "message_id": "m2"}, "", 2},
		{"conflicting aliases", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "event_id": "other"}, map[string]any{"event_id": "same"}, "", 2},
		{"multiple common keys are one candidate", "gen_ai.user.message", "gen_ai.user.message", map[string]any{"event.id": "same", "gen_ai.message.id": "m"}, map[string]any{"event_id": "same", "message_id": "m"}, "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := setup(t)
			i := recorded(t, "i", "import", "copilot", "s", tc.ik, tc.ia)
			o := recorded(t, "o", "otel", "copilot", "s", tc.ok, tc.oa)
			if tc.signal != "" {
				o.Signal = tc.signal
			}
			save(t, a, i, o)
			if p := page(t, a, "copilot:s", "", 0); p.Count != tc.want {
				t.Fatal(p)
			}
		})
	}
}
func TestAmbiguousIDsStayVisible(t *testing.T) {
	a := setup(t)
	i := recorded(t, "i", "import", "claude", "s", "gen_ai.user.message", map[string]any{"event.id": "x"})
	o := recorded(t, "o", "otel", "claude", "s", "user_prompt", map[string]any{"event_id": "x"})
	save(t, a, i, o)
	if p := page(t, a, "claude:s", "", 0); p.Count != 1 {
		t.Fatal(p)
	}
	another := o
	another.ID = "another"
	save(t, a, another)
	if p := page(t, a, "claude:s", "", 0); p.Count != 3 {
		t.Fatal("ambiguous records hidden", p)
	}
}
func TestUpdatedImportReevaluatesMatching(t *testing.T) {
	a := setup(t)
	i := recorded(t, "i", "import", "claude", "s", "gen_ai.user.message", map[string]any{"event.id": "x"})
	o := recorded(t, "o", "otel", "claude", "s", "user_prompt", map[string]any{"event_id": "x"})
	save(t, a, i, o)
	if p := page(t, a, "claude:s", "", 0); p.Count != 1 {
		t.Fatal(p)
	}
	i = recorded(t, "i", "import", "claude", "s", "gen_ai.user.message", map[string]any{"event.id": "y"})
	save(t, a, i)
	if p := page(t, a, "claude:s", "", 0); p.Count != 2 {
		t.Fatal("stale match", p)
	}
	o = recorded(t, "other", "otel", "claude", "s", "user_prompt", map[string]any{"event_id": "y"})
	save(t, a, o)
	if p := page(t, a, "claude:s", "", 0); p.Count != 2 {
		t.Fatal(p)
	}
}
func TestMatchingBeforePagination(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for n := range 205 {
		for _, origin := range []string{"import", "otel"} {
			e := recorded(t, fmt.Sprintf("%s:%d", origin, n), origin, "codex", "paged", "gen_ai.user.message", map[string]any{"event.id": fmt.Sprint(n), "prompt": "needle"})
			e.Time = fmt.Sprintf("2026-11-14T00:00:%02d.%09dZ", n/100, n)
			if origin == "otel" {
				e.Time = strings.Replace(e.Time, "00:00:", "00:01:", 1)
			}
			records = append(records, e)
		}
	}
	save(t, a, records...)
	first := page(t, a, "codex:paged", "", 0)
	last := page(t, a, "codex:paged", "", 200)
	if first.Count != 205 || len(first.Events) != 201 || last.Count != 205 || len(last.Events) != 5 {
		t.Fatal(first.Count, len(first.Events), last.Count, len(last.Events))
	}
	for _, e := range last.Events {
		if len(e.Records) != 2 {
			t.Fatal("cross-page duplicate", e)
		}
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?q=needle", &sessions)
	if len(sessions) != 1 || sessions[0].Count != 205 {
		t.Fatal(sessions)
	}
	filtered := page(t, a, "codex:paged", "otel", 200)
	if filtered.Count != 205 || len(filtered.Events) != 5 || len(filtered.Events[0].Records) != 1 {
		t.Fatal(filtered)
	}
}

func TestMergedPreviewAndToolTitle(t *testing.T) {
	a := setup(t)
	i := recorded(t, "user", "import", "claude", "s", "gen_ai.user.message", map[string]any{"event.id": "u"})
	o := recorded(t, "user-o", "otel", "claude", "s", "user_prompt", map[string]any{"event_id": "u", "prompt": "Visible in both views"})
	save(t, a, i, o)
	var summaries []SessionSummary
	getJSON(t, a, "/api/sessions", &summaries)
	if len(summaries) != 1 || !hasBlock(*summaries[0].Preview, "Request", "Visible in both views") {
		t.Fatal(summaries)
	}
	p := page(t, a, "claude:s", "", 0)
	if !hasBlock(*p.Preview, "Request", "Visible in both views") {
		t.Fatal(p)
	}
	i = recorded(t, "tool", "import", "claude", "s", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "c", "cwd": "/work/a", "output": "result"})
	o = recorded(t, "tool-o", "otel", "claude", "s", "tool_result", map[string]any{"tool_use_id": "c", "tool_name": "Bash", "working_directory": "/work/b"})
	save(t, a, i, o)
	p = page(t, a, "claude:s", "", 0)
	var tool Detail
	for _, e := range p.Events {
		if e.Detail.Kind == "tool" {
			tool = e.Detail
		}
	}
	if tool.Title != "Bash" {
		t.Fatal(tool)
	}
	found := false
	for _, c := range tool.Conflicts {
		if c.Field == "Working directory" {
			found = true
		}
	}
	if !found {
		t.Fatal("alias conflict omitted", tool)
	}
}
func TestCompletedSpanMatchesOnlyResult(t *testing.T) {
	a := setup(t)
	i := recorded(t, "result", "import", "copilot", "s", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "c", "output": "done"})
	start := recorded(t, "start", "import", "copilot", "s", "gen_ai.tool.start", map[string]any{"gen_ai.tool.call.id": "c"})
	o := recorded(t, "span", "otel", "copilot", "s", "execute_tool", map[string]any{"gen_ai.tool.call.id": "c", "gen_ai.tool.name": "shell"})
	o.Signal = "traces"
	d, err := decodeDocument(o.Raw)
	if err != nil {
		t.Fatal(err)
	}
	d.Record["startTimeUnixNano"] = "1794614400000000000"
	d.Record["endTimeUnixNano"] = "1794614401200000000"
	o.Raw, _ = json.Marshal(d)
	save(t, a, i, start, o)
	p := page(t, a, "copilot:s", "", 0)
	if p.Count != 2 {
		t.Fatal(p)
	}
	for _, e := range p.Events {
		if len(e.Records) == 2 && !hasField(e.Detail, "Duration", "1.20 s") {
			t.Fatal(e)
		}
	}
	d.Record["endTimeUnixNano"] = nil
	o.Raw, _ = json.Marshal(d)
	o.ID = "partial"
	save(t, a, o)
	if p = page(t, a, "copilot:s", "", 0); p.Count != 3 {
		t.Fatal(p)
	}
}
func TestTraceReassignmentReevaluatesMatching(t *testing.T) {
	a := setup(t)
	i := recorded(t, "result", "import", "codex", "s", "gen_ai.tool.result", map[string]any{"call_id": "c"})
	o := recorded(t, "native", "otel", "codex", "unused", "tool_result", map[string]any{"call_id": "c"})
	o.Session = "trace:codex:trace"
	o.Trace = "trace"
	o.Span = "child"
	o.ParentSpan = "root"
	save(t, a, i, o)
	if p := page(t, a, "codex:s", "", 0); p.Count != 1 || len(p.Events[0].Records) != 1 {
		t.Fatal(p)
	}
	root := recorded(t, "root", "otel", "codex", "s", "root", nil)
	root.Trace = "trace"
	root.Span = "root"
	save(t, a, root)
	p := page(t, a, "codex:s", "", 0)
	if p.Count != 2 {
		t.Fatal(p)
	}
	found := false
	for _, e := range p.Events {
		if len(e.Records) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("not matched after trace ownership resolved", p)
	}
}
func TestEmptyOriginView(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "i", "import", "codex", "s", "gen_ai.user.message", map[string]any{"event.id": "x"}))
	p := page(t, a, "codex:s", "otel", 0)
	if p.Count != 0 || len(p.Events) != 0 || p.Preview != nil {
		t.Fatal(p)
	}
}
