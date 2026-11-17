package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestPromptTitleEnvelopes(t *testing.T) {
	for _, test := range []struct{ source, text, want string }{
		{"codex", "<recommended_plugins>\nshared plugins\n</recommended_plugins>", ""},
		{"codex", "<recommended_plugins>plugins</recommended_plugins>\n<environment_context>cwd</environment_context>\n\n一覧のタイトルを修正して\n追加説明", "一覧のタイトルを修正して"},
		{"codex", "# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>rules</INSTRUCTIONS>\n<skill><name>review</name></skill>\n最初の依頼", "最初の依頼"},
		{"codex", "\n\n実際のユーザーメッセージ\n次の行", "実際のユーザーメッセージ"},
		{"codex", "<recommended_plugins> の意味を教えて", "<recommended_plugins> の意味を教えて"},
		{"codex", "<div>このHTMLを修正して</div>", "<div>このHTMLを修正して</div>"},
		{"claude", "<recommended_plugins>literal</recommended_plugins>", "<recommended_plugins>literal</recommended_plugins>"},
		{"codex", "Content not exported", ""},
		{"codex", selectedPageSnapshot, ""},
		{"codex", selectedPageSnapshot + "\n最初の依頼", "最初の依頼"},
		{"codex", "<recommended_plugins>shared</recommended_plugins>" + selectedPageSnapshot, ""},
		{"codex", "<external_codex_apps_selected_page_part_2_of_2>context</external_codex_apps_selected_page_part_2_of_2>", ""},
		{"codex", "このログを説明して:\n" + selectedPageSnapshot, "このログを説明して:"},
		{"codex", "<external_codex_apps_selected_page_part_1_of_1> の意味を教えて", "<external_codex_apps_selected_page_part_1_of_1> の意味を教えて"},
		{"codex", "<external_codex_apps_selected_page_part_1_of_2>x</external_codex_apps_selected_page_part_2_of_2>", "<external_codex_apps_selected_page_part_1_of_2>x</external_codex_apps_selected_page_part_2_of_2>"},
		{"codex", "<external_codex_apps_selected_page_part_2_of_1>x</external_codex_apps_selected_page_part_2_of_1>", "<external_codex_apps_selected_page_part_2_of_1>x</external_codex_apps_selected_page_part_2_of_1>"},
		{"claude", selectedPageSnapshot, strings.Split(selectedPageSnapshot, "\n")[0]},
		{"codex", `<external_codex_apps_open_page>{"page_id":null}</external_codex_apps_open_page>`, ""},
		{"codex", "<external_codex_apps_open_page>{\"page_id\":\"page-123\"}</external_codex_apps_open_page>\n検索を修正して", "検索を修正して"},
		{"codex", "<recommended_plugins>plugins</recommended_plugins>\n<external_codex_apps_open_page>{}</external_codex_apps_open_page>\n本来の依頼", "本来の依頼"},
		{"codex", "<external_codex_apps_open_page> の意味を教えて", "<external_codex_apps_open_page> の意味を教えて"},
		{"codex", "このタグを調べて: <external_codex_apps_open_page>{}</external_codex_apps_open_page>", "このタグを調べて: <external_codex_apps_open_page>{}</external_codex_apps_open_page>"},
		{"claude", "<external_codex_apps_open_page>{}</external_codex_apps_open_page>", "<external_codex_apps_open_page>{}</external_codex_apps_open_page>"},
	} {
		t.Run(test.text[:min(len(test.text), 24)], func(t *testing.T) {
			got := promptTitle(test.source, Detail{Kind: "user", Blocks: []Block{{Label: "Request", Text: test.text}}})
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestPromptTitleRespectsStructuredMessageRoles(t *testing.T) {
	detail := Detail{Kind: "user", Attributes: map[string]any{"gen_ai.input.messages": []any{
		map[string]any{"role": "system", "content": "Shared system instructions"},
		map[string]any{"role": "assistant", "content": "Prior assistant reply"},
		map[string]any{"role": "user", "content": "実際の依頼"},
	}}}
	if got := promptTitle("codex", detail); got != "実際の依頼" {
		t.Fatal("non-user message used as title", got)
	}
	detail.Attributes["prompt"] = "<recommended_plugins>shared context</recommended_plugins>"
	if got := promptTitle("codex", detail); got != "実際の依頼" {
		t.Fatal("context-only prompt masked structured user message", got)
	}
}

func previewTitle(t *testing.T, a *App, session, origin string) string {
	t.Helper()
	var title string
	err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		detail, err := sessionPreview(context.Background(), tx, session, origin)
		if err != nil {
			return err
		}
		for _, block := range detail.Blocks {
			if block.Label == "Request" {
				title = block.Text
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return title
}

func TestFirstHumanPromptTitlesAndOrigins(t *testing.T) {
	a := setup(t)
	context := recorded(t, "context", "import", "codex", "title", "gen_ai.user.message", map[string]any{"prompt": "<recommended_plugins>shared plugins</recommended_plugins>", "cwd": "/work/title"})
	first := recorded(t, "first", "import", "codex", "title", "gen_ai.user.message", map[string]any{"prompt": "最初の依頼\n詳細"})
	first.Sequence = 1
	later := recorded(t, "later", "otel", "codex", "title", "user_prompt", map[string]any{"prompt": "後の依頼"})
	later.Sequence = 2
	// Ingestion order must not replace the earliest user request with the latest.
	save(t, a, later, first, context)
	if got := previewTitle(t, a, "codex:title", ""); got != "最初の依頼" {
		t.Fatal(got)
	}
	if got := previewTitle(t, a, "codex:title", "otel"); got != "後の依頼" {
		t.Fatal("origin isolation", got)
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].Preview.Blocks[0].Text != "最初の依頼" {
		t.Fatal("catalog still contains context", sessions)
	}
	if got := page(t, a, "codex:title", "", 0); got.Count != 3 || !strings.Contains(got.Events[0].Detail.Blocks[0].Text, "recommended_plugins") {
		t.Fatal("context disappeared from timeline", got.Count)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	if got := previewTitle(t, a, "codex:title", ""); got != "後の依頼" {
		t.Fatal("deleted first prompt remained in catalog", got)
	}
}

func TestContextOnlySessionHasNoFabricatedPrompt(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "context", "otel", "codex", "context-only", "user_prompt", map[string]any{"prompt": "<recommended_plugins>plugins</recommended_plugins>"}))
	if got := previewTitle(t, a, "codex:context-only", ""); got != "" {
		t.Fatal("context used as a request", got)
	}
}

func TestModelOnlyConversationKeepsExplicitUserInputTitle(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "model", "otel", "copilot", "model-only", "gen_ai.client.operation", map[string]any{
		"gen_ai.operation.name": "chat",
		"gen_ai.input.messages": `[{"role":"system","content":"shared system instructions"},{"role":"user","content":"Fix the search query"}]`,
	}))
	if got := previewTitle(t, a, "copilot:model-only", ""); got != "Fix the search query" {
		t.Fatal("model-only user input lost", got)
	}
}

func appOpenPageMessage(t *testing.T, pageID string) Event {
	t.Helper()
	return appContextMessage(t, "<external_codex_apps_open_page>{\"page_id\":"+pageID+"}</external_codex_apps_open_page>")
}

func appContextMessage(t *testing.T, text string) Event {
	t.Helper()
	e := recorded(t, "app-open-page", "import", "codex", "app-titles", "gen_ai.user.message", map[string]any{"cwd": "/work/project"})
	doc, err := decodeDocument(e.Raw)
	if err != nil {
		t.Fatal(err)
	}
	var attribute map[string]any
	content, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise OTLP arrayValue/kvlistValue, matching imported Codex messages.
	if err = decodeJSON([]byte(`{"key":"gen_ai.input.messages","value":{"arrayValue":{"values":[{"kvlistValue":{"values":[{"key":"role","value":{"stringValue":"user"}},{"key":"parts","value":{"arrayValue":{"values":[{"kvlistValue":{"values":[{"key":"type","value":{"stringValue":"text"}},{"key":"content","value":{"stringValue":`+string(content)+`}}]}}]}}}]}}]}}}`), &attribute); err != nil {
		t.Fatal(err)
	}
	doc.Record["attributes"] = append(arr(doc.Record["attributes"]), attribute)
	e.Raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAppMessageIsPreservedButNotUsedAsSessionTitle(t *testing.T) {
	a := setup(t)
	auto := appOpenPageMessage(t, "null")
	save(t, a, auto)
	if got := previewTitle(t, a, auto.Session, ""); got != "" {
		t.Fatal("automatic message became a title", got)
	}
	human := recorded(t, "human", "import", "codex", "app-titles", "gen_ai.user.message", map[string]any{"prompt": "検索の表示を修正して"})
	human.Sequence = 1
	save(t, a, human)
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].Preview.Blocks[0].Text != "検索の表示を修正して" {
		t.Fatal("first human prompt not displayed", sessions)
	}
	var originals []Event
	getJSON(t, a, "/api/export?session="+auto.Session, &originals)
	if len(originals) != 2 || string(originals[0].Raw) != string(auto.Raw) {
		t.Fatal("automatic message was removed or changed")
	}
}
