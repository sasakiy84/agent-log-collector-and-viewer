package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
)

func TestActivityFiltersBeforePagination(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for i := 0; i < 410; i++ {
		e := recorded(t, fmt.Sprintf("prep-%03d", i), "otel", "codex", "activity", "prepare_tools", nil)
		e.Sequence = i
		records = append(records, e)
	}
	records = append(records, recorded(t, "user", "otel", "codex", "activity", "codex.user_prompt", map[string]any{"prompt": "hello"}), recorded(t, "answer", "otel", "codex", "activity", "codex.agent_response", map[string]any{"response": "actual answer"}), recorded(t, "failure", "otel", "codex", "activity", "internal_failure", map[string]any{"error": "failure remains visible"}))
	save(t, a, records...)
	var p SessionPage
	getJSON(t, a, "/api/session?id=codex:activity&view=activity&limit=2&position=latest", &p)
	if p.TotalCount != 413 || p.Count != 3 || p.Offset != 2 || len(p.Events) != 1 {
		t.Fatalf("wrong filtered page: %+v", p)
	}
	getJSON(t, a, "/api/session?id=codex:activity&view=activity&kind=conversation", &p)
	if p.Count != 2 || len(p.Events) != 2 {
		t.Fatal(p)
	}
	for _, e := range p.Events {
		if e.Detail.Kind == "assistant" && !hasBlock(e.Detail, "Response", "actual answer") {
			t.Fatal(e)
		}
	}
	getJSON(t, a, "/api/session?id=codex:activity&view=diagnostics&kind=tool", &p)
	if p.Count != 0 || len(p.Events) != 0 {
		t.Fatal("preparation spans treated as tools", p)
	}
	getJSON(t, a, "/api/session?id=codex:activity&view=diagnostics&position=latest", &p)
	if p.Count != 413 || len(p.Events) != 13 || p.Offset != 400 {
		t.Fatal(p)
	}
}

func TestActivityKeepsEmbeddedConversationsAndSkillReferences(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "embedded", "otel", "copilot", "chat", "chat", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.input.messages": `[{"role":"user","content":"question"}]`, "gen_ai.output.messages": `[{"role":"assistant","content":"answer"}]`}), recorded(t, "reference", "import", "codex", "skills", "gen_ai.tool.start", map[string]any{"tool_name": "exec_command", "arguments": `{"cmd":"cat /work/skills/review/SKILL.md"}`}))
	var p SessionPage
	getJSON(t, a, "/api/session?id=copilot:chat&view=activity&kind=conversation", &p)
	if p.Count != 1 || len(p.Events) != 1 {
		t.Fatal("embedded conversation hidden", p)
	}
	getJSON(t, a, "/api/session?id=codex:skills&view=activity&kind=skill", &p)
	if p.Count != 1 || len(p.Events) != 1 || len(p.Events[0].Skills) != 1 {
		t.Fatal("skill reference hidden", p)
	}
}

func TestPresentationUpgradeDoesNotReprocessCurrentCards(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "current", "otel", "codex", "s", "codex.user_prompt", map[string]any{"prompt": "question"}), recorded(t, "legacy", "otel", "codex", "s", "codex.agent_response", map[string]any{"response": "answer"}))
	// Simulate the one old card left by an interrupted upgrade. Newly generated
	// cards already use the current parser and must not be parsed a second time.
	if _, err := a.DB.Exec("UPDATE event_cards SET presentation_ready=0,kind='model' WHERE id='legacy'"); err != nil {
		t.Fatal(err)
	}
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		_, n, err := backfillPresentation(context.Background(), tx, "")
		if err == nil && n != 1 {
			return fmt.Errorf("reprocessed current cards: %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var p SessionPage
	getJSON(t, a, "/api/session?id=codex:s&view=activity&kind=conversation", &p)
	if p.Count != 2 {
		t.Fatal(p)
	}
}

func modelSpan(t *testing.T, id, session, span, parent, name string, values map[string]any) Event {
	t.Helper()
	e := recorded(t, id, "otel", "codex", session, name, values)
	e.Signal = "traces"
	e.Trace = "explicit-trace"
	e.Span = span
	e.ParentSpan = parent
	doc, err := decodeDocument(e.Raw)
	if err != nil {
		t.Fatal(err)
	}
	doc.Record["startTimeUnixNano"] = "1790000000000000000"
	doc.Record["endTimeUnixNano"] = "1790000002000000000"
	e.Raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestModelCallsUseExplicitAncestryAndLatestAnswer(t *testing.T) {
	a := setup(t)
	answer := recorded(t, "answer", "otel", "codex", "calls", "codex.agent_response", map[string]any{"response": "answer", "turn.id": "turn"})
	fragment := recorded(t, "fragment", "otel", "codex", "calls", "codex.agent_response", map[string]any{"response": "progress", "turn.id": "turn"})
	fragment.Sequence = -1
	usage := modelSpan(t, "usage", "calls", "usage-span", "middle", "handle_responses", map[string]any{"input_tokens": "100", "output_tokens": "5"})
	root := modelSpan(t, "request", "calls", "root", "", "run_sampling_request", map[string]any{"turn.id": "turn", "model": "test-model"})
	middle := modelSpan(t, "middle", "calls", "middle", "root", "transport", nil)
	save(t, a, answer, fragment, usage)
	p := page(t, a, "codex:calls", "", 0)
	for _, e := range p.Events {
		if len(e.ModelCalls) != 0 {
			t.Fatal("unlinked usage was attributed", e)
		}
	}
	save(t, a, root) // Missing intermediate ancestry must remain unresolved.
	save(t, a, middle)
	check := func() {
		t.Helper()
		var p SessionPage
		getJSON(t, a, "/api/session?id=codex:calls&view=activity", &p)
		for _, e := range p.Events {
			switch e.ID {
			case "answer":
				if len(e.ModelCalls) != 1 || e.ModelCalls[0].Values["input_tokens"] != "100" || e.ModelCalls[0].Duration != "2000" {
					t.Fatal(e)
				}
			case "fragment":
				if len(e.ModelCalls) != 0 {
					t.Fatal("statistics repeated on fragment", e)
				}
			}
		}
	}
	check()
	// Repeated reports are totals. A different session using the same trace/span
	// identifiers cannot supply usage to this session's request.
	other := modelSpan(t, "other", "other", "other-span", "root", "handle_responses", map[string]any{"input_tokens": "999"})
	save(t, a, usage, other)
	check()
	var inspected TimelineEvent
	getJSON(t, a, "/api/event?session=codex:calls&id=request", &inspected)
	if len(inspected.ModelCalls) != 1 || inspected.ModelCalls[0].Values["output_tokens"] != "5" {
		t.Fatal(inspected)
	}
	usageWant(t, sessionUsage(t, a, "codex:calls", ""), "duration_samples", "1")
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM events WHERE id='usage'"); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	inspected = TimelineEvent{}
	getJSON(t, a, "/api/event?session=codex:calls&id=request", &inspected)
	if len(inspected.ModelCalls) != 1 || len(inspected.ModelCalls[0].Values) != 0 {
		t.Fatal("deleted usage remains", inspected)
	}
}

func TestProjectsNavigationAndSessionFiltering(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "a", "otel", "codex", "a", "codex.user_prompt", map[string]any{"prompt": "first", "cwd": "/work/a", "service.namespace": "Named"}), recorded(t, "b", "import", "claude", "b", "user_prompt", map[string]any{"prompt": "second", "service.namespace": "Named"}), recorded(t, "c", "otel", "codex", "c", "codex.user_prompt", map[string]any{"prompt": "third", "cwd": "/one/app"}), recorded(t, "d", "otel", "codex", "d", "codex.user_prompt", map[string]any{"prompt": "fourth", "cwd": "/two/app"}), recorded(t, "e", "otel", "codex", "e", "codex.user_prompt", map[string]any{"prompt": "read /not/the/project/SKILL.md"}))
	var projects struct {
		Projects        []ProjectSummary
		Total, Sessions int
	}
	getJSON(t, a, "/api/projects", &projects)
	if projects.Total != 3 || projects.Sessions != 5 {
		t.Fatal(projects)
	}
	var sessions []SessionSummary
	for key, count := range map[string]int{"name:Named": 2, "name:app": 2, "path:/one/app": 2, "path:/two/app": 2, "": 1} {
		getJSON(t, a, "/api/sessions?project="+url.QueryEscape(key), &sessions)
		if len(sessions) != count {
			t.Fatal(key, sessions)
		}
	}
	getJSON(t, a, "/api/projects?q=Named&source=codex", &projects)
	if projects.Total != 1 || projects.Projects[0].Count != 1 {
		t.Fatal(projects)
	}
	// Project cache follows changed labels and removal of the last member.
	save(t, a, recorded(t, "b", "import", "claude", "b", "user_prompt", map[string]any{"prompt": "second", "service.namespace": "Moved"}))
	getJSON(t, a, "/api/projects?q=Named", &projects)
	if projects.Total != 1 || projects.Projects[0].Count != 1 {
		t.Fatal(projects)
	}
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM events WHERE id='a'"); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	getJSON(t, a, "/api/projects?q=Named", &projects)
	if projects.Total != 0 {
		t.Fatal(projects)
	}
}
