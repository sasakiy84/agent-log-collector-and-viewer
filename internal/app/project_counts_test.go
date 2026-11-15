package app

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestProjectCountsMatchVisibleSessionsAndRecency(t *testing.T) {
	a := setup(t)
	root := recorded(t, "root", "otel", "codex", "root", "user_prompt", map[string]any{"prompt": "hello", "service.namespace": "shop"})
	second := recorded(t, "second", "otel", "claude", "second", "user_prompt", map[string]any{"prompt": "hello", "service.namespace": "shop"})
	children := []Event{}
	for i := 0; i < 39; i++ {
		child := recorded(t, fmt.Sprint(i), "import", "codex", fmt.Sprint(i), "user_prompt", map[string]any{"prompt": "child", "service.namespace": "shop"})
		child.Parent = root.Session
		child.Time = "2026-11-16T00:00:00.000000000Z"
		children = append(children, child)
	}
	save(t, a, append([]Event{root, second}, children...)...)
	check := func(params string, want int, end string) {
		t.Helper()
		var projects struct {
			Projects        []ProjectSummary
			Total, Sessions int
		}
		getJSON(t, a, "/api/projects?"+params, &projects)
		if projects.Total != 1 || projects.Sessions != want || projects.Projects[0].Count != want || projects.Projects[0].End != end {
			t.Fatal(params, projects, want, end)
		}
	}
	check("", 2, root.Time)
	check("include_children=1", 41, children[0].Time)
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?project=name:shop", &sessions)
	if len(sessions) != 2 {
		t.Fatal("fixture did not reproduce hidden children", len(sessions))
	}
	check("source=codex", 1, root.Time)
	check("source=claude", 1, second.Time)
	// Internal root conversations are also excluded until explicitly requested.
	auto := recorded(t, "auto", "otel", "codex", "auto", "codex.user_prompt", map[string]any{"prompt": automaticSuggestion, "service.namespace": "shop"})
	auto.Time = "2026-11-15T00:00:00.000000000Z"
	save(t, a, auto)
	check("", 2, root.Time)
	check("diagnostics=1", 3, auto.Time)
	check("diagnostics=1&include_children=1", 42, children[0].Time)
	// A parent-role change updates counts even though the total is unchanged.
	promoted := recorded(t, "0", "import", "codex", "0", "user_prompt", map[string]any{"prompt": "promoted", "service.namespace": "shop"})
	promoted.Time = children[0].Time
	save(t, a, promoted)
	check("", 3, promoted.Time)
	demoted := recorded(t, "0", "import", "codex", "0", "user_prompt", map[string]any{"prompt": "demoted", "service.namespace": "shop"})
	demoted.Parent = root.Session
	demoted.Time = promoted.Time
	save(t, a, demoted)
	check("", 2, root.Time)
	// Removing the newest visible root leaves child data but no phantom count.
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM events WHERE id='root'"); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	check("", 1, second.Time)
	check("include_children=1", 40, children[0].Time)
}

func TestProjectsContainingOnlyHiddenSessionsAreNotListed(t *testing.T) {
	a := setup(t)
	child := recorded(t, "child", "otel", "codex", "child", "user_prompt", map[string]any{"prompt": "child", "service.namespace": "children-only"})
	child.Parent = "codex:parent"
	auto := recorded(t, "auto", "otel", "codex", "auto", "codex.user_prompt", map[string]any{"prompt": automaticSuggestion, "service.namespace": "internal-only"})
	save(t, a, child, auto)
	var projects struct {
		Projects        []ProjectSummary
		Total, Sessions int
	}
	getJSON(t, a, "/api/projects", &projects)
	if projects.Total != 0 || projects.Sessions != 0 || len(projects.Projects) != 0 {
		t.Fatal("hidden-only projects counted", projects)
	}
	getJSON(t, a, "/api/projects?diagnostics=1", &projects)
	if projects.Total != 1 || projects.Sessions != 1 || projects.Projects[0].ID != "name:internal-only" {
		t.Fatal("explicit internal visibility differs from session list", projects)
	}
	getJSON(t, a, "/api/projects?include_children=1&diagnostics=1", &projects)
	if projects.Total != 2 || projects.Sessions != 2 {
		t.Fatal("inclusive export scope was lost", projects)
	}
}
