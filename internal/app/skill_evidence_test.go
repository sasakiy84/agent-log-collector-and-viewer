package app

import (
	"fmt"
	"net/url"
	"testing"
)

func TestSkillAttemptsIncludeRequestsReportsAndInjections(t *testing.T) {
	a := setup(t)
	read := func(id, origin, session string) Event {
		return recorded(t, id, origin, "codex", session, "gen_ai.tool.start", map[string]any{"tool_name": "Read", "call_id": id, "arguments": `{"file_path":"/work/skills/writing/SKILL.md"}`, "service.namespace": "docs"})
	}
	events := []Event{}
	for i := 0; i < 145; i++ {
		events = append(events, read(fmt.Sprintf("read-%d", i), "import", fmt.Sprintf("session-%d", i)))
	}
	// Requests count even when no result was exported.
	events = append(events, read("reread", "import", "session-0"), read("native-read", "otel", "session-0"))
	// A structured injection also establishes an attempt.
	events = append(events, recorded(t, "injection", "import", "codex", "session-0", "gen_ai.user.message", map[string]any{"service.namespace": "docs", "prompt": "<skill><name>writing</name><path>/work/skills/writing/SKILL.md</path></skill>"}))
	save(t, a, events...)
	check := func(want int) {
		t.Helper()
		for _, query := range []string{"", "?project=name:docs", "?q=" + url.QueryEscape("/work/skills")} {
			var p skillCatalogPage
			getJSON(t, a, "/api/skills"+query, &p)
			if p.Total != 1 || p.Attempts != want || p.Skills[0].Sessions != 145 {
				t.Fatalf("%s: %+v", query, p)
			}
		}
	}
	check(148)
	save(t, a, skillReport(t, "report-1", "otel", "session-0", "writing", "one", ""), skillReport(t, "retry", "otel", "session-0", "writing", "one", ""), skillReport(t, "report-2", "otel", "session-0", "writing", "two", ""), skillReport(t, "imported-report", "import", "session-0", "writing", "one", ""))
	// Same-turn reports and origin copies count once.
	check(150)
	for _, origin := range []string{"", "otel", "import"} {
		p := page(t, a, "codex:session-0", origin, 0)
		want := 6
		if origin == "import" {
			want = 4
		} else if origin == "otel" {
			want = 3
		}
		if len(p.Skills) != 1 || p.Skills[0].Attempts != want {
			t.Fatal(origin, p.Skills)
		}
	}
	// Reclassification on deletion uses current evidence, without a stale cache.
	if _, err := a.DB.Exec("DELETE FROM records WHERE id IN('report-1','retry','report-2','import:imported-report')"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	check(148)
	p := page(t, a, "codex:session-0", "", 0)
	if len(p.Skills) != 1 || p.Skills[0].Attempts != 4 {
		t.Fatal(p.Skills)
	}
}
