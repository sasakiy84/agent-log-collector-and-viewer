package app

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
)

type skillCatalogPage struct {
	Skills []SkillSummary `json:"skills"`
	Total  int            `json:"total"`
	Uses   int            `json:"uses"`
	Reads  int            `json:"reads"`
	Offset int            `json:"offset"`
	Sort   string         `json:"sort"`
}

func TestSkillUsageCatalogProjectScopeAndOrdering(t *testing.T) {
	a := setup(t)
	for _, session := range []string{"a1", "a2", "a3", "b1", "b2", "b3"} {
		project := "A"
		if session[0] == 'b' {
			project = "B"
		}
		save(t, a, recorded(t, "prompt-"+session, "otel", "codex", session, "codex.user_prompt", map[string]any{"prompt": "question", "service.namespace": project}))
	}
	rows := []Event{
		skillReport(t, "review-a1-1", "otel", "a1", "review", "one", ""), skillReport(t, "retry", "otel", "a1", "review", "one", ""), skillReport(t, "review-a1-2", "otel", "a1", "review", "two", ""),
		skillReport(t, "review-a2", "otel", "a2", "review", "one", ""), skillReport(t, "review-b1", "otel", "b1", "review", "one", ""),
		skillReport(t, "build-1", "otel", "a1", "build", "one", ""), skillReport(t, "build-2", "otel", "a1", "build", "two", ""), skillReport(t, "build-3", "otel", "a1", "build", "three", ""),
		recorded(t, "history-a", "import", "codex", "a3", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "arguments": `{"file_path":"/only-A/skills/history/SKILL.md"}`}),
		recorded(t, "history-b", "import", "codex", "b3", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "arguments": `{"file_path":"/only-B/skills/history/SKILL.md"}`}),
	}
	newest := skillReport(t, "newest", "otel", "b2", "newest", "one", "")
	newest.Time = "2026-11-15T00:00:00.000000000Z"
	rows = append(rows, newest)
	save(t, a, rows...)
	var p skillCatalogPage
	getJSON(t, a, "/api/skills", &p)
	if p.Total != 4 || p.Uses != 8 || p.Reads != 0 || p.Sort != "reads" || p.Skills[0].Name != "review" || p.Skills[0].Uses != 4 || p.Skills[0].Sessions != 3 {
		t.Fatal(p)
	}
	getJSON(t, a, "/api/skills?project=name:A", &p)
	if p.Total != 3 || p.Uses != 6 || p.Reads != 0 || p.Skills[0].Name != "review" || p.Skills[0].Uses != 3 || p.Skills[0].Sessions != 2 {
		t.Fatal(p)
	}
	getJSON(t, a, "/api/skills?sort=recent", &p)
	if p.Sort != "recent" || p.Skills[0].Name != "newest" || p.Uses != 8 || p.Reads != 0 {
		t.Fatal(p)
	}
	getJSON(t, a, "/api/skills?sort=sessions", &p)
	if p.Skills[0].Name != "review" || p.Skills[1].Name != "history" || p.Uses != 8 || p.Reads != 0 {
		t.Fatal(p)
	}
	getJSON(t, a, "/api/skills?q="+url.QueryEscape("ＲＥＶＩＥＷ"), &p)
	if p.Total != 1 || p.Uses != 4 {
		t.Fatal(p)
	}
	getJSON(t, a, "/api/skills?project=name:A&q=only-B", &p)
	if p.Total != 0 || p.Uses != 0 || len(p.Skills) != 0 {
		t.Fatal("path match from another project", p)
	}
	getJSON(t, a, "/api/skills?project=name:B&q=only-B", &p)
	if p.Total != 1 || p.Uses != 0 || p.Reads != 0 || p.Skills[0].Pending != 1 {
		t.Fatal(p)
	}
	// Archive-locked sessions contribute neither reported usage nor search metadata.
	if _, err := a.DB.Exec("INSERT INTO archive_jobs(id,status,created_at,cutoff) VALUES('skill-test','preparing','',''); INSERT INTO archive_locks VALUES('codex:b3','skill-test')"); err != nil {
		t.Fatal(err)
	}
	getJSON(t, a, "/api/skills?q=only-B", &p)
	if p.Total != 0 || p.Uses != 0 {
		t.Fatal("archived path matched", p)
	}
	r := httptest.NewRequest("GET", "/api/skills?sort=invalid", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestSkillUsageTotalCoversAllPages(t *testing.T) {
	a := setup(t)
	rows := []Event{}
	for i := 0; i < 205; i++ {
		if i%2 == 0 {
			rows = append(rows, recorded(t, fmt.Sprint(i), "import", "codex", "session", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "arguments": fmt.Sprintf(`{"file_path":"/work/skills/skill-%03d/SKILL.md"}`, i)}))
		} else {
			rows = append(rows, skillReport(t, fmt.Sprint(i), "otel", "session", fmt.Sprintf("skill-%03d", i), "one", ""))
		}
	}
	save(t, a, rows...)
	for _, offset := range []int{0, 200, 9999} {
		var p skillCatalogPage
		getJSON(t, a, fmt.Sprintf("/api/skills?offset=%d", offset), &p)
		want := 200
		if offset > 0 {
			want = 5
		}
		if p.Total != 205 || p.Uses != 102 || p.Reads != 0 || len(p.Skills) != want {
			t.Fatal(offset, p)
		}
	}
}
