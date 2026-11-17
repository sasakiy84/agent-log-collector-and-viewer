package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestFacetedSessionSearchAndAnalysisUseTheSameRecords(t *testing.T) {
	a := setup(t)
	at := func(id, source, session, project, when, name string, attrs map[string]any) Event {
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs["service.namespace"] = project
		e := recorded(t, id, "otel", source, session, name, attrs)
		e.Time = when
		if parent, ok := attrs["parent.session.id"].(string); ok {
			e.Parent = "codex:" + parent
		}
		return e
	}
	const begin = "2026-11-14T00:00:00.000000000Z"
	const end = "2026-11-15T00:00:00.000000000Z"
	rows := []Event{
		at("a-old", "codex", "a", "A", "2026-11-13T23:59:59.999999999Z", "codex.user_prompt", map[string]any{"prompt": "needle outside", "call_id": "old-call"}),
		at("a-start", "codex", "a", "A", begin, "codex.user_prompt", map[string]any{"prompt": "alpha 認証"}),
		at("a-tool", "codex", "a", "A", "2026-11-14T12:00:00.000000000Z", "gen_ai.tool.result", map[string]any{"tool_name": "exec_command", "output": "alpha beta failed", "exit_code": 1, "error": "failed", "call_id": "inside-call"}),
		at("a-end", "codex", "a", "A", end, "codex.user_prompt", map[string]any{"prompt": "boundary outside"}),
		at("b", "claude", "b", "B", begin, "gen_ai.user.message", map[string]any{"prompt": "beta 認証"}),
		at("c", "copilot", "c", "C", begin, "gen_ai.user.message", map[string]any{"prompt": "alpha beta"}),
		at("old", "codex", "old", "A", "2026-11-13T00:00:00.000000000Z", "codex.user_prompt", map[string]any{"prompt": "alpha beta needle"}),
		at("child", "codex", "child", "A", begin, "codex.user_prompt", map[string]any{"prompt": "alpha beta", "parent.session.id": "a"}),
	}
	// The importer-origin copy must not satisfy a native-origin text filter.
	imported := recorded(t, "imported", "import", "codex", "a", "gen_ai.tool.start", map[string]any{"tool_name": "exec_command", "arguments": `{"cmd":"echo imported-only"}`})
	imported.Time = begin
	rows = append(rows, imported)
	save(t, a, rows...)
	window := "from=" + url.QueryEscape(begin) + "&to=" + url.QueryEscape(end)
	sessions := func(q string, want ...string) {
		t.Helper()
		var out []SessionSummary
		getJSON(t, a, "/api/sessions?"+q, &out)
		got := []string{}
		for _, s := range out {
			got = append(got, s.ID)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%s got %v want %v", q, got, want)
		}
	}
	sessions(window, "codex:a", "claude:b", "copilot:c")
	sessions(window+"&project=name:A&project=name:B&source=codex&source=claude", "codex:a", "claude:b")
	sessions(window + "&q=needle")
	sessions(window + "&q=boundary")
	sessions(window+"&q=alpha+beta", "codex:a", "copilot:c")
	sessions(window+"&q=alpha+beta&text_mode=any", "codex:a", "claude:b", "copilot:c")
	sessions(window+"&q=認証&project=name:A", "codex:a")
	sessions(window+"&event_kind=error", "codex:a")
	sessions(window+"&event_kind=error&event_kind=conversation", "codex:a", "claude:b", "copilot:c")
	sessions(window+"&q=alpha+beta&exclude=failed", "copilot:c")
	sessions(window + "&q=imported-only&origin=otel")
	sessions(window+"&q=imported-only&origin=import", "codex:a")
	sessions(window + "&id_key=call.id&id_value=old-call")
	sessions(window + "&id_key=call.id&id_value=inside-call&q=認証")
	sessions(window+"&id_key=call.id&id_value=inside-call&q=failed", "codex:a")
	// Offset timestamps normalize to the same inclusive/exclusive UTC boundaries.
	sessions("from="+url.QueryEscape("2026-11-14T09:00:00+09:00")+"&to="+url.QueryEscape("2026-11-15T09:00:00+09:00"), "codex:a", "claude:b", "copilot:c")
	var analysis struct {
		Groups []struct {
			Key                       string
			Records, Sessions, Errors int
		}
	}
	getJSON(t, a, "/api/search/summary?"+window+"&q=alpha+beta&group=project", &analysis)
	if len(analysis.Groups) != 2 {
		t.Fatal(analysis)
	}
	for _, g := range analysis.Groups {
		if g.Records != 1 || g.Sessions != 1 || (g.Key == "name:A" && g.Errors != 1) {
			t.Fatal(g)
		}
	}
	var projects struct{ Projects []ProjectSummary }
	getJSON(t, a, "/api/projects?"+window+"&session_q=認証", &projects)
	if len(projects.Projects) != 2 {
		t.Fatal(projects)
	}
	for _, p := range projects.Projects {
		if p.Count != 1 {
			t.Fatal(p)
		}
	}
	// Search and range probes must use existing FTS / card range indexes.
	f := parseSessionFilter(url.Values{"from": {begin}, "to": {end}})
	query, args, err := f.sql(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a.DB.Query("EXPLAIN QUERY PLAN SELECT s.session"+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	details := ""
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err = plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details += detail + "\n"
	}
	if !strings.Contains(details, "cards_page") || !strings.Contains(details, "time>?") {
		t.Fatal(details)
	}
}

func TestFacetedSearchRejectsInvalidParameters(t *testing.T) {
	a := setup(t)
	for _, endpoint := range []string{"/api/sessions", "/api/skills", "/api/projects", "/api/search/summary", "/api/export/sessions", "/api/export/download"} {
		for _, q := range []string{"from=bad", "to=", "from=2026-11-15T00:00:00Z&to=2026-11-14T00:00:00Z", "source=typo", "origin=typo"} {
			r := httptest.NewRequest("GET", endpoint+"?"+q, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("%s?%s: %d %s", endpoint, q, w.Code, w.Body.String())
			}
		}
	}
}

func TestSkillTimeWindowKeepsDedupeWithinSelectedEvidence(t *testing.T) {
	a := setup(t)
	const begin = "2026-11-14T00:00:00.000000000Z"
	const end = "2026-11-15T00:00:00.000000000Z"
	read := recorded(t, "read", "import", "codex", "a", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "arguments": `{"file_path":"/work/skills/review/SKILL.md"}`, "service.namespace": "A"})
	read.Time = begin
	older := skillReport(t, "older", "otel", "a", "review", "old", "")
	older.Time = "2026-11-13T00:00:00.000000000Z"
	anchor := recorded(t, "anchor", "otel", "codex", "a", "codex.user_prompt", map[string]any{"prompt": "review", "service.namespace": "A"})
	anchor.Time = begin
	save(t, a, read, older, anchor)
	window := "?from=" + url.QueryEscape(begin) + "&to=" + url.QueryEscape(end)
	check := func(q string, attempts int) {
		t.Helper()
		var p skillCatalogPage
		getJSON(t, a, "/api/skills"+q, &p)
		if p.Attempts != attempts {
			t.Fatalf("%s: %+v", q, p)
		}
	}
	check(window, 1) // Native reports outside the window do not suppress reads.
	imported := skillReport(t, "inside", "import", "a", "review", "inside", "")
	imported.Time = begin
	boundary := skillReport(t, "boundary", "otel", "a", "review", "outside", "")
	boundary.Time = end
	save(t, a, imported, boundary)
	check(window, 2) // Neither out-of-range native report suppresses this import.
	check(window+"&origin=import", 2)
	check(window+"&origin=otel", 0)
	native := skillReport(t, "native", "otel", "a", "review", "inside", "")
	native.Time = begin
	save(t, a, native)
	check(window, 2)
	other := skillReport(t, "other", "otel", "b", "build", "inside", "")
	other.Time = begin
	prompt := recorded(t, "prompt", "otel", "codex", "b", "codex.user_prompt", map[string]any{"prompt": "build", "service.namespace": "B"})
	prompt.Time = begin
	save(t, a, other, prompt)
	check(window+"&project=name:A&project=name:B", 3)
	check(window+"&q=review+build&text_mode=any", 3)
	check(window+"&q=review+build&text_mode=all", 0)
	check(window+"&exclude=build", 2)
	// The complete skill catalog is read-only; no raw reparse is needed.
	if _, err := a.DB.Exec("UPDATE records SET raw='not JSON' WHERE id='import:read'"); err != nil {
		t.Fatal(err)
	}
	check(window, 2)
	var body map[string]json.RawMessage
	getJSON(t, a, "/api/skills"+window, &body)
	if len(body["attempts"]) == 0 {
		t.Fatal(body)
	}
}
