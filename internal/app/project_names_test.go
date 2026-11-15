package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
)

func TestProjectNamesMergeExplicitLabelsAndDirectoryNames(t *testing.T) {
	a := setup(t)
	for i, test := range []struct{ directory, label, key string }{
		{"/work/shop", "", "name:shop"},
		{"/another/shop/", "", "name:shop"},
		{`C:\work\shop\`, "", "name:shop"},
		{"shop", "", "name:shop"},
		{"/not/shop", "shop", "name:shop"},
		{"/work/unrelated", "Explicit", "name:Explicit"},
		{"/work/日本語/", "", "name:日本語"},
		{"/", "", "name:/"},
		{`C:\`, "", "name:C:"},
		{"", "", ""},
	} {
		e := recorded(t, fmt.Sprint(i), "otel", "codex", fmt.Sprint(i), "user_prompt", map[string]any{"prompt": "hello", "cwd": test.directory, "service.namespace": test.label})
		save(t, a, e)
		var key string
		if err := a.DB.QueryRow("SELECT project_key FROM session_catalog WHERE session=?", e.Session).Scan(&key); err != nil || key != test.key {
			t.Fatal(test, key, err)
		}
		if test.directory != "" && test.label == "" && canonicalProjectID("path:"+test.directory) != key {
			t.Fatal("URL and database project names differ", test.directory, key)
		}
	}
	var projects struct {
		Projects        []ProjectSummary
		Total, Sessions int
	}
	getJSON(t, a, "/api/projects?q=shop", &projects)
	if projects.Total != 1 || projects.Projects[0].ID != "name:shop" || projects.Projects[0].Count != 5 {
		t.Fatal("same names split by directory", projects)
	}
}

func TestMergedProjectCountsSkillsExportsAndSubsequentChanges(t *testing.T) {
	a := setup(t)
	events := []Event{}
	for i := 0; i < 47; i++ {
		origin := "otel"
		attrs := map[string]any{"prompt": "hello", "service.namespace": "shop"}
		if i >= 41 {
			origin = "import"
			attrs = map[string]any{"prompt": "hello", "cwd": "/workspaces/shop"}
		}
		events = append(events, recorded(t, fmt.Sprint(i), origin, "codex", fmt.Sprint(i), "user_prompt", attrs))
	}
	events = append(events, skillReport(t, "named-skill", "otel", "0", "review", "one", ""), skillReport(t, "directory-skill", "otel", "41", "review", "one", ""))
	save(t, a, events...)
	check := func(count int) {
		t.Helper()
		var projects struct {
			Projects []ProjectSummary
			Total    int
		}
		getJSON(t, a, "/api/projects?q=shop", &projects)
		if projects.Total != 1 || projects.Projects[0].Count != count {
			t.Fatal("incorrect merged count", projects)
		}
		var sessions []SessionSummary
		getJSON(t, a, "/api/sessions?project=name:shop", &sessions)
		if len(sessions) != count {
			t.Fatal("merged selection omitted sessions", len(sessions), count)
		}
	}
	check(47)
	var skills skillCatalogPage
	for _, key := range []string{"name:shop", "path:/workspaces/shop"} {
		getJSON(t, a, "/api/skills?"+url.Values{"project": {key}, "q": {"review"}}.Encode(), &skills)
		if skills.Uses != 2 || len(skills.Skills) != 1 || skills.Skills[0].Sessions != 2 {
			t.Fatal("skills did not merge", key, skills)
		}
	}
	exported := archiveReq(a, "GET", "/api/export/download?"+url.Values{"project": {"path:/workspaces/shop"}}.Encode(), "")
	manifest, _ := decodeExport(t, exported.Body.Bytes())
	if manifest.Sessions != 47 || manifest.Records != 49 || manifest.Filters["project"] != "name:shop" {
		t.Fatal("export did not use the merged group", manifest)
	}
	// Moving within a same-named directory keeps membership; renaming it moves
	// one session, and removal subtracts from the same canonical cached group.
	moved := recorded(t, "46", "import", "codex", "46", "user_prompt", map[string]any{"prompt": "hello", "cwd": "/elsewhere/shop/"})
	moved.Time = "2026-11-15T00:00:00.000000000Z"
	save(t, a, moved)
	check(47)
	var ordered struct{ Projects []ProjectSummary }
	getJSON(t, a, "/api/projects", &ordered)
	if ordered.Projects[0].End != moved.Time {
		t.Fatal("merged group lost latest activity", ordered)
	}
	save(t, a, recorded(t, "46", "import", "codex", "46", "user_prompt", map[string]any{"prompt": "hello", "cwd": "/elsewhere/renamed"}))
	check(46)
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM events WHERE id='2'"); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	check(45)
}
