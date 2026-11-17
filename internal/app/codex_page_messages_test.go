package app

import (
	"testing"
)

const selectedPageSnapshot = `<external_codex_apps_selected_page_part_1_of_1>Companion Page snapshot part 1 of 1:
{"page_id":null,"document_view":null}</external_codex_apps_selected_page_part_1_of_1>`

func TestCodexPageContextVisibilityAndHumanTitle(t *testing.T) {
	a := setup(t)
	auto := appContextMessage(t, "<recommended_plugins>shared</recommended_plugins>"+selectedPageSnapshot)
	save(t, a, auto)
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 0 {
		t.Fatal("page-only session visible by default", sessions)
	}
	var projects struct{ Sessions int }
	getJSON(t, a, "/api/projects", &projects)
	if projects.Sessions != 0 {
		t.Fatal("project count includes page-only session", projects)
	}
	getJSON(t, a, "/api/sessions?diagnostics=1", &sessions)
	if len(sessions) != 1 {
		t.Fatal("internal session unavailable", sessions)
	}
	human := recorded(t, "human", "import", "codex", "app-titles", "gen_ai.user.message", map[string]any{"prompt": "最初のユーザーの依頼"})
	human.Sequence = 1
	save(t, a, human)
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || len(sessions[0].Preview.Blocks) != 1 || sessions[0].Preview.Blocks[0].Text != "最初のユーザーの依頼" {
		t.Fatal("human conversation hidden or snapshot title retained", sessions)
	}
	var originals []Event
	getJSON(t, a, "/api/export?session="+auto.Session, &originals)
	if len(originals) != 2 || string(originals[0].Raw) != string(auto.Raw) {
		t.Fatal("original context changed", originals)
	}
}
