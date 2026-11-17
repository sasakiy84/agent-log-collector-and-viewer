package app

import (
	"fmt"
	"net/url"
	"testing"
)

func TestSkillReadAttemptsNeverInventUses(t *testing.T) {
	a := setup(t)
	read := func(id, origin, session string) Event {
		return recorded(t, id, origin, "codex", session, "gen_ai.tool.start", map[string]any{"tool_name": "Read", "call_id": id, "arguments": `{"file_path":"/work/skills/writing/SKILL.md"}`, "service.namespace": "docs"})
	}
	events := []Event{}
	for i := 0; i < 145; i++ {
		events = append(events, read(fmt.Sprintf("read-%d", i), "import", fmt.Sprintf("session-%d", i)))
	}
	// Missing results never establish invocations or successful reads.
	events = append(events, read("reread", "import", "session-0"), read("native-read", "otel", "session-0"))
	// A structured injection is a separate type of evidence.
	events = append(events, recorded(t, "injection", "import", "codex", "session-0", "gen_ai.user.message", map[string]any{"service.namespace": "docs", "prompt": "<skill><name>writing</name><path>/work/skills/writing/SKILL.md</path></skill>"}))
	save(t, a, events...)
	check := func(want int) {
		t.Helper()
		for _, query := range []string{"", "?project=name:docs", "?q=" + url.QueryEscape("/work/skills")} {
			var p skillCatalogPage
			getJSON(t, a, "/api/skills"+query, &p)
			if p.Total != 1 || p.Uses != want || p.Reads != 0 || p.Skills[0].Sessions != 145 || p.Skills[0].Pending != 147 {
				t.Fatalf("%s: %+v", query, p)
			}
		}
	}
	check(0)
	save(t, a, skillReport(t, "report-1", "otel", "session-0", "writing", "one", ""), skillReport(t, "retry", "otel", "session-0", "writing", "one", ""), skillReport(t, "report-2", "otel", "session-0", "writing", "two", ""), skillReport(t, "imported-report", "import", "session-0", "writing", "one", ""))
	// Only the two reported turns count as uses.
	check(2)
	for _, origin := range []string{"", "otel", "import"} {
		p := page(t, a, "codex:session-0", origin, 0)
		want := 2
		if origin == "import" {
			want = 1
		}
		if len(p.Skills) != 1 || p.Skills[0].Uses != want || p.Skills[0].Reads != 0 {
			t.Fatal(origin, p.Skills)
		}
	}
	// Reclassification on deletion uses current evidence, without a stale cache.
	if _, err := a.DB.Exec("DELETE FROM records WHERE id IN('report-1','retry','report-2','import:imported-report')"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	check(0)
	p := page(t, a, "codex:session-0", "", 0)
	if len(p.Skills) != 1 || p.Skills[0].Uses != 0 || p.Skills[0].Pending != 3 {
		t.Fatal(p.Skills)
	}
}
