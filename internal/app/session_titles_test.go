package app

import (
	"context"
	"database/sql"
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
	if _, err := a.DB.Exec("DELETE FROM events WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	if got := previewTitle(t, a, "codex:title", ""); got != "後の依頼" {
		t.Fatal("deleted first prompt remained in catalog", got)
	}
}

func TestTitleBackfillReadsFullPromptAndResumes(t *testing.T) {
	a := setup(t)
	text := "<recommended_plugins>\n" + strings.Repeat("shared context\n", 1000) + "</recommended_plugins>\n本当の最初の依頼"
	event := recorded(t, "full-prompt", "import", "codex", "long", "gen_ai.user.message", map[string]any{"prompt": text})
	save(t, a, event)
	if _, err := a.DB.Exec("UPDATE event_cards SET prompt_title=''; UPDATE maintenance_jobs SET cursor='',complete=0 WHERE name=?", titlesJob); err != nil {
		t.Fatal(err)
	}
	if err := a.runMaintenance(context.Background(), titlesJob, backfillTitles, 0); err != nil {
		t.Fatal(err)
	}
	if got := previewTitle(t, a, "codex:long", ""); got != "本当の最初の依頼" {
		t.Fatal("title was derived from truncated card", got)
	}
	var raw []byte
	if err := a.DB.QueryRow("SELECT raw FROM events WHERE id=?", event.ID).Scan(&raw); err != nil || string(raw) != string(event.Raw) {
		t.Fatal("original changed", err)
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
