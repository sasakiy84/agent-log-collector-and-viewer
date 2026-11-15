package app

import (
	"context"
	"database/sql"
	"strings"
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

func TestCodexPageContextUpgradeRepairsTitlesFactsAndCheckpoint(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	auto := appContextMessage(t, selectedPageSnapshot)
	human := recorded(t, "human", "import", "codex", "app-titles", "gen_ai.user.message", map[string]any{"prompt": "本来の依頼"})
	human.Sequence = 1
	only := recorded(t, "only", "otel", "codex", "only", "codex.user_prompt", map[string]any{"prompt": selectedPageSnapshot})
	open := recorded(t, "open", "otel", "codex", "open", "codex.user_prompt", map[string]any{"prompt": "<external_codex_apps_open_page>{}</external_codex_apps_open_page>"})
	save(t, a, auto, human, only, open)
	if _, err := a.DB.Exec("UPDATE codex_session_facts SET automatic=0,human=1 WHERE event_id IN (?,?,?)", auto.ID, only.ID, open.ID); err != nil {
		t.Fatal(err)
	}
	// The old open-page title was already empty; the numbered snapshot was not.
	if _, err := a.DB.Exec("UPDATE event_cards SET prompt_title=? WHERE id IN (?,?)", strings.Split(selectedPageSnapshot, "\n")[0], auto.ID, only.ID); err != nil {
		t.Fatal(err)
	}
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		for _, e := range []Event{auto, only} {
			preview, err := catalogPreview(ctx, tx, e.Session)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("UPDATE session_catalog SET preview=?,internal=0 WHERE session=?", preview, e.Session); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec("UPDATE maintenance_jobs SET complete=0,cursor='' WHERE name=?", appPageMessagesJob); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || len(sessions[0].Preview.Blocks) != 1 || sessions[0].Preview.Blocks[0].Text != "本来の依頼" {
		t.Fatal("persisted titles or visibility not repaired", sessions)
	}
	var projects struct{ Sessions int }
	getJSON(t, a, "/api/projects", &projects)
	if projects.Sessions != 1 {
		t.Fatal("cached counts not repaired", projects)
	}
	for _, e := range []Event{auto, only, open} {
		var raw []byte
		if err := a.DB.QueryRow("SELECT raw FROM events WHERE id=?", e.ID).Scan(&raw); err != nil || string(raw) != string(e.Raw) {
			t.Fatal("original modified", e.ID, err)
		}
	}
	if err := a.runMaintenance(ctx, appPageMessagesJob, func(context.Context, *sql.Tx, string) (string, int, error) {
		t.Fatal("completed repair repeated")
		return "", 0, nil
	}, 0); err != nil {
		t.Fatal(err)
	}
}
