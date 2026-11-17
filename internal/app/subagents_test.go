package app

import (
	"context"
	"fmt"
	"net/url"
	"testing"
)

func TestSubagentLaunchAcrossPagesAndLateResult(t *testing.T) {
	a := setup(t)
	start := recorded(t, "launch", "import", "codex", "parent", "gen_ai.tool.start", map[string]any{"gen_ai.tool.name": "functions.spawn_agent", "gen_ai.tool.call.id": "spawn", "gen_ai.tool.call.arguments": `{"message":"Review"}`})
	save(t, a, start)
	if len(page(t, a, "codex:parent", "", 0).Events[0].Subagents) != 0 {
		t.Fatal("invented child before a result")
	}
	batch := []Event{}
	for i := 1; i <= 205; i++ {
		e := recorded(t, fmt.Sprint(i), "otel", "codex", "parent", "gen_ai.assistant.message", map[string]any{"response": "message"})
		e.Sequence = i
		batch = append(batch, e)
	}
	result := recorded(t, "result", "import", "codex", "parent", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "spawn", "gen_ai.tool.call.result": `{"agent_id":"child","nickname":"Reviewer"}`})
	result.Sequence = 300
	batch = append(batch, result)
	save(t, a, batch...)
	var first SessionPage
	getJSON(t, a, "/api/session?id=codex:parent&limit=1", &first)
	if len(first.Events) != 2 || len(first.Events[0].Subagents) != 1 || first.Events[0].Subagents[0].Session != "codex:child" || first.Events[0].Subagents[0].Label != "Reviewer" {
		t.Fatalf("launch link: %+v", first.Events)
	}
	var original TimelineEvent
	getJSON(t, a, "/api/event?session=codex:parent&id="+url.QueryEscape(start.ID), &original)
	if len(original.Subagents) != 1 {
		t.Fatal("missing link in event inspector")
	}
	// A changed import removes stale references, including those on the request.
	changed := recorded(t, "result", "import", "codex", "parent", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "spawn", "gen_ai.tool.call.result": `{"agent_id":"revised"}`})
	save(t, a, changed)
	getJSON(t, a, "/api/session?id=codex:parent&limit=1", &first)
	if first.Events[0].Subagents[0].Session != "codex:revised" {
		t.Fatal(first.Events[0].Subagents)
	}
}

func TestSubagentScopeAndClaudeResult(t *testing.T) {
	a := setup(t)
	for _, source := range []string{"codex", "claude"} {
		tool := "spawn_agent"
		output := `{"agent_id":"reviewer"}`
		if source == "claude" {
			tool = "Task"
			output = "Review finished.\nagentId: reviewer (for resuming this agent)"
		}
		start := recorded(t, "start-"+source, "otel", source, "root", "gen_ai.tool.start", map[string]any{"tool_name": tool, "call_id": "shared"})
		result := recorded(t, "result-"+source, "otel", source, "root", "gen_ai.tool.result", map[string]any{"call_id": "shared", "output": output})
		wrong := recorded(t, "wrong-"+source, "otel", source, "other", "gen_ai.tool.result", map[string]any{"call_id": "shared", "output": `{"agent_id":"wrong"}`})
		fake := recorded(t, "fake-"+source, "otel", source, "root", "gen_ai.tool.result", map[string]any{"tool_name": "exec_command", "call_id": "unrelated", "output": `{"agent_id":"not-an-agent-launch"}`})
		save(t, a, result, wrong, fake, start)
		p := page(t, a, source+":root", "", 0)
		for _, event := range p.Events {
			if event.ID == fake.ID {
				if len(event.Subagents) != 0 {
					t.Fatal("arbitrary tool JSON became link")
				}
				continue
			}
			if len(event.Subagents) != 1 {
				t.Fatalf("%s %+v", source, event)
			}
			want := source + ":reviewer"
			if source == "claude" {
				want = "claude:root/agent:reviewer"
			}
			if event.Subagents[0].Session != want {
				t.Fatal(event.Subagents)
			}
		}
	}
}

func TestSubagentCopilotInlinePaginationAndDedup(t *testing.T) {
	a := setup(t)
	batch := []Event{recorded(t, "invocation", "otel", "copilot", "root", "gen_ai.agent.started", map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.id": "reviewer", "gen_ai.agent.name": "Reviewer"})}
	for i := 0; i < 85; i++ {
		agent := "reviewer"
		if i >= 45 {
			agent = "other"
		}
		e := recorded(t, fmt.Sprint(i), "otel", "copilot", "root", "gen_ai.assistant.message", map[string]any{"agent_id": agent, "response": fmt.Sprint(i), "event.id": fmt.Sprint(i)})
		e.Sequence = i
		batch = append(batch, e)
	}
	duplicate := recorded(t, "duplicate", "import", "copilot", "root", "gen_ai.assistant.message", map[string]any{"agent_id": "reviewer", "response": "0", "event.id": "0"})
	save(t, a, append(batch, duplicate)...)
	var p SessionPage
	getJSON(t, a, "/api/subagent?session=copilot:root&agent=reviewer&position=latest", &p)
	if p.Count != 45 || p.Offset != 40 || len(p.Events) != 5 || p.Events[0].Detail.Blocks[0].Text != "40" {
		t.Fatalf("inline page: %+v", p)
	}
	for _, event := range p.Events {
		for _, record := range event.Records {
			if len(record.Raw) > 0 {
				t.Fatal("loaded raw JSON for preview")
			}
		}
	}
	getJSON(t, a, "/api/subagent?session=copilot:root&agent=reviewer&offset=0", &p)
	if len(p.Events) != 40 || len(p.Events[0].Records) != 2 {
		t.Fatal("duplicate import/OTEL counted twice")
	}
	root := page(t, a, "copilot:root", "", 0)
	for _, event := range root.Events {
		if event.ID == "invocation" {
			if len(event.Subagents) != 1 || event.Subagents[0].Agent != "reviewer" {
				t.Fatal(event.Subagents)
			}
		}
	}
	if _, err := a.DB.ExecContext(context.Background(), "DELETE FROM records"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := a.DB.QueryRow("SELECT (SELECT COUNT(*) FROM agent_calls)+(SELECT COUNT(*) FROM agent_activity)").Scan(&n); err != nil || n != 0 {
		t.Fatalf("index did not cascade: %d %v", n, err)
	}
}

func TestSubagentOwnerScopeAndStructuredResults(t *testing.T) {
	a := setup(t)
	start := recorded(t, "a", "otel", "copilot", "root", "gen_ai.tool.start", map[string]any{"tool_name": "runSubagent", "call_id": "same", "agent_id": "caller-a"})
	wrong := recorded(t, "b", "otel", "copilot", "root", "gen_ai.tool.result", map[string]any{"call_id": "same", "agent_id": "caller-b", "output": `{"agent_id":"wrong-child"}`})
	save(t, a, start, wrong)
	for _, event := range page(t, a, "copilot:root", "", 0).Events {
		if len(event.Subagents) != 0 {
			t.Fatal("crossed executing-agent scope", event.Subagents)
		}
	}
	result := recorded(t, "c", "otel", "copilot", "root", "gen_ai.tool.result", map[string]any{"call_id": "same", "agent_id": "caller-a", "output": `{"content":[{"type":"text","text":"{\"agent_id\":\"right-child\",\"nickname\":\"Reviewer\"}"}]}`})
	save(t, a, result)
	for _, event := range page(t, a, "copilot:root", "", 0).Events {
		if event.ID == wrong.ID {
			continue
		}
		if len(event.Subagents) != 1 || event.Subagents[0].Agent != "right-child" {
			t.Fatal("structured launch output", event.Subagents)
		}
	}
}

func TestSubagentRuntimeTraceSession(t *testing.T) {
	a := setup(t)
	e := recorded(t, "trace-agent", "otel", "copilot", "unused", "gen_ai.assistant.message", map[string]any{"agent_id": "reviewer", "response": "Trace-linked child work"})
	e.Session = "trace:copilot:abc"
	save(t, a, e)
	var p SessionPage
	getJSON(t, a, "/api/subagent?session=trace%3Acopilot%3Aabc&agent=reviewer", &p)
	if p.Count != 1 || p.Events[0].Detail.Blocks[0].Text != "Trace-linked child work" {
		t.Fatal(p)
	}
}
