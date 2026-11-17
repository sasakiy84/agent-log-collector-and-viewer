package app

import (
	"encoding/json"
	"os"
	"testing"
)

func copilotRecordedTrace(t *testing.T) []Event {
	t.Helper()
	raw, err := os.ReadFile("testdata/copilot-1.0.112-traces.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	events, err := normalize(payload, "traces")
	if err != nil {
		t.Fatal(err)
	}
	return events
}
func TestCopilotRecordedTraceReadableActivityAndUsage(t *testing.T) {
	a := setup(t)
	events := copilotRecordedTrace(t)
	save(t, a, events...)
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].Preview.Blocks[0].Text != "Invoke the review skill, use view to read fixture.txt, then say Fixture read complete." {
		t.Fatal(sessions)
	}
	usage := sessionUsage(t, a, "copilot:fixture-session", "")
	usageWant(t, usage, "input_tokens", "300")
	usageWant(t, usage, "output_tokens", "30")
	usageWant(t, usage, "cache_read_tokens", "60")
	var activity SessionPage
	getJSON(t, a, "/api/session?id=copilot:fixture-session&view=activity", &activity)
	if activity.Count != 3 {
		t.Fatalf("activity: %+v", activity)
	}
	var agent, tool bool
	for _, e := range activity.Events {
		if e.Detail.Kind == "agent" {
			agent = true
			if len(e.Subagents) != 0 || len(e.ModelCalls) != 3 {
				t.Fatal("main invocation is not a subagent", e)
			}
			if !hasBlock(e.Detail, "assistant", "Fixture read complete.") {
				t.Fatal(e.Detail)
			}
		}
		if e.Detail.Title == "view" {
			tool = true
			if !hasBlock(e.Detail, "Output", "Synthetic file contents for Agentlog display test.\n") {
				t.Fatal(e.Detail)
			}
			full := inspected(t, a, "copilot:fixture-session", e.ID, "")
			if full.ToolPair.State != "embedded" {
				t.Fatal(full.ToolPair)
			}
		}
	}
	if !agent || !tool {
		t.Fatal(agent, tool)
	}
	var skills skillCatalogPage
	getJSON(t, a, "/api/skills", &skills)
	if skills.Total != 1 || skills.Uses != 1 || skills.Reads != 0 || skills.Skills[0].Name != "review" {
		t.Fatal(skills)
	}
	var diagnostics SessionPage
	getJSON(t, a, "/api/session?id=copilot:fixture-session&view=diagnostics", &diagnostics)
	if diagnostics.Count != 6 {
		t.Fatal(diagnostics.Count)
	}
}
func TestClaudeDocumentedToolSkillAndResponseFormats(t *testing.T) {
	a := setup(t)
	span := modelSpan(t, "read", "root", "read-span", "", "claude_code.tool", map[string]any{"tool_name": "Read", "tool_use_id": "call-read", "file_path": "/work/skills/review/SKILL.md", "tool_input": `{"file_path":"/work/skills/review/SKILL.md"}`, "session.id": "root"})
	var raw map[string]any
	if err := json.Unmarshal(span.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	obj(raw["record"])["events"] = []any{map[string]any{"name": "tool.output", "attributes": []any{map[string]any{"key": "content", "value": map[string]any{"stringValue": "Read the project's test guide."}}}}}
	span.Source = "claude"
	span.Session = "claude:root"
	obj(raw["resource"])["attributes"] = []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "claude-code"}}}
	span.Raw, _ = json.Marshal(raw)
	prompt := recorded(t, "prompt", "otel", "claude", "root", "claude_code.user_prompt", map[string]any{"prompt_text": "Inspect the tests", "message.uuid": "user-message"})
	reply := recorded(t, "reply", "otel", "claude", "root", "claude_code.assistant_response", map[string]any{"response": "The tests passed."})
	report := recorded(t, "skill", "otel", "claude", "root", "claude_code.skill_activated", map[string]any{"skill.name": "review", "skill.source": "projectSettings", "invocation_trigger": "user-slash", "plugin.name": "tools"})
	stats := recorded(t, "stats", "otel", "claude", "root", "claude_code.api_request", map[string]any{"input_tokens": 100, "output_tokens": 20, "cache_read_tokens": 40, "cache_creation_tokens": 10, "request_id": "req-one", "model": "claude-sonnet", "cost_usd": "0.02"})
	trace := modelSpan(t, "model", "root", "model-span", "", "claude_code.llm_request", map[string]any{"input_tokens": 100, "output_tokens": 20, "cache_read_tokens": 40, "cache_creation_tokens": 10, "gen_ai.response.id": "req-one"})
	trace.Source = "claude"
	trace.Session = "claude:root"
	save(t, a, prompt, reply, span, report, stats, trace)
	var p SessionPage
	getJSON(t, a, "/api/session?id=claude:root&view=activity", &p)
	if p.Count != 4 {
		t.Fatal(p.Count, p.Events)
	}
	full := inspected(t, a, "claude:root", span.ID, "")
	if !hasBlock(full.Detail, "Output", "Read the project's test guide.") || full.ToolPair.State != "embedded" {
		t.Fatal(full)
	}
	var skills skillCatalogPage
	getJSON(t, a, "/api/skills", &skills)
	if skills.Total != 2 {
		t.Fatal(skills)
	} // Named plugin invocation and independent file-read evidence.
	for _, s := range skills.Skills {
		if s.Plugin == "tools" && (s.Uses != 1 || s.Reads != 0) {
			t.Fatal(s)
		}
	}
	usage := sessionUsage(t, a, "claude:root", "")
	usageWant(t, usage, "input_tokens", "150")
	usageWant(t, usage, "output_tokens", "20")
	var matches []SessionSummary
	getJSON(t, a, "/api/sessions?id_key=message.id&id_value=user-message", &matches)
	if len(matches) != 1 {
		t.Fatal(matches)
	}
}
func TestCopilotMultipleSkillInvocationsAndAgentTotalsAreNotDuplicated(t *testing.T) {
	a := setup(t)
	root := modelSpan(t, "root", "root", "root-span", "", "invoke_agent", map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.id": "github.copilot.default", "gen_ai.usage.input_tokens": 1000, "gen_ai.usage.output_tokens": 200, "github.copilot.nano_aiu": "1500000000"})
	root.Source = "copilot"
	root.Session = "copilot:root"
	var raw map[string]any
	json.Unmarshal(root.Raw, &raw)
	events := []any{}
	for i := 0; i < 2; i++ {
		events = append(events, map[string]any{"name": "github.copilot.skill.invoked", "attributes": []any{map[string]any{"key": "gen_ai.skill.name", "value": map[string]any{"stringValue": "review"}}}})
	}
	obj(raw["record"])["events"] = events
	root.Raw, _ = json.Marshal(raw)
	child := modelSpan(t, "child", "root", "child-span", "root-span", "chat", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": 100, "gen_ai.usage.output_tokens": 20, "github.copilot.nano_aiu": "1500000000"})
	child.Source = "copilot"
	child.Session = "copilot:root"
	save(t, a, root, child)
	u := sessionUsage(t, a, "copilot:root", "")
	usageWant(t, u, "input_tokens", "100")
	usageWant(t, u, "output_tokens", "20")
	usageWant(t, u, "nano_aiu", "1500000000")
	var p skillCatalogPage
	getJSON(t, a, "/api/skills", &p)
	if p.Uses != 2 || p.Reads != 0 {
		t.Fatal(p)
	}
	save(t, a, root, child)
	getJSON(t, a, "/api/skills", &p)
	if p.Uses != 2 {
		t.Fatal("replay counted", p)
	}
}

func TestCopilotTraceSubagentsUseInstancesNotDefinitionNames(t *testing.T) {
	a := setup(t)
	trace := "33333333333333333333333333333333"
	makeSpan := func(id, span, parent, name string, attributes map[string]any) Event {
		e := modelSpan(t, id, "root", span, parent, name, attributes)
		e.Source = "copilot"
		e.Session = "copilot:root"
		e.Trace = trace
		return e
	}
	attrs := func(name string) map[string]any {
		return map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": name, "gen_ai.agent.id": "same-agent-definition"}
	}
	first := makeSpan("first", "0000000000000001", "0000000000000000", "invoke_agent", attrs("Explore"))
	second := makeSpan("second", "0000000000000002", "0000000000000000", "invoke_agent", attrs("Explore"))
	tool := makeSpan("tool", "0000000000000003", first.Span, "execute_tool", map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "view", "gen_ai.tool.call.arguments": `{"path":"/work/fixture.txt"}`, "gen_ai.tool.call.result": `{"content":"first agent only"}`})
	other := makeSpan("other", "0000000000000004", second.Span, "execute_tool", map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "view", "gen_ai.tool.call.result": "second agent only"})
	save(t, a, first, second, tool, other)
	p := page(t, a, "copilot:root", "", 0)
	for _, event := range p.Events {
		if event.Detail.Kind == "agent" {
			var title string
			a.DB.QueryRow("SELECT prompt_title FROM record_facts WHERE id=?", event.ID).Scan(&title)
			if title != "" {
				t.Fatal("subagent prompt became session title", title)
			}
		}
	}
	for _, event := range p.Events {
		if event.ID == first.ID || event.ID == second.ID {
			if len(event.Subagents) != 1 || event.Subagents[0].Agent != "trace:"+trace+":"+event.Span {
				t.Fatal(event.Subagents)
			}
		}
	}
	var preview SessionPage
	getJSON(t, a, "/api/subagent?session=copilot:root&agent=trace:"+trace+":"+first.Span, &preview)
	if preview.Count != 2 || len(preview.Events) != 2 {
		t.Fatal(preview)
	}
	for _, event := range preview.Events {
		if event.ID == second.ID || event.ID == other.ID || len(event.Subagents) > 0 {
			t.Fatal("crossed instance or self-link", event)
		}
	}
	getJSON(t, a, "/api/subagent?session=copilot:root&agent=trace:44444444444444444444444444444444:"+first.Span, &preview)
	if preview.Count != 0 {
		t.Fatal("crossed trace", preview.Count)
	}
}

func TestProviderUnassignedTelemetryHiddenFromDefaultNavigation(t *testing.T) {
	a := setup(t)
	events := []Event{}
	for _, source := range []string{"claude", "copilot"} {
		e := recorded(t, source, "otel", source, "temporary", "gen_ai.event", nil)
		e.Session = "unassigned:" + source
		events = append(events, e)
	}
	save(t, a, events...)
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 0 {
		t.Fatal(sessions)
	}
	var projects struct{ Projects []ProjectSummary }
	getJSON(t, a, "/api/projects", &projects)
	if len(projects.Projects) != 0 {
		t.Fatal(projects)
	}
	getJSON(t, a, "/api/sessions?diagnostics=1", &sessions)
	if len(sessions) != 2 {
		t.Fatal(sessions)
	}
}

func TestCopilotNestedSkillSearchUsesInvocationTime(t *testing.T) {
	a := setup(t)
	root := modelSpan(t, "nested", "root", "nested-span", "", "invoke_agent", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	root.Source = "copilot"
	root.Session = "copilot:root"
	root.Time = "2026-11-13T23:59:00.000000000Z"
	var raw map[string]any
	json.Unmarshal(root.Raw, &raw)
	obj(raw["record"])["events"] = []any{map[string]any{"name": "github.copilot.skill.invoked", "timeUnixNano": "1794618000000000000", "attributes": []any{map[string]any{"key": "gen_ai.skill.name", "value": map[string]any{"stringValue": "review"}}}}} // 2026-11-14 01:00 UTC
	root.Raw, _ = json.Marshal(raw)
	save(t, a, root)
	const scope = "from=2026-11-14T00:30:00Z&to=2026-11-14T01:30:00Z"
	var p skillCatalogPage
	getJSON(t, a, "/api/skills?"+scope, &p)
	if p.Uses != 1 {
		t.Fatal(p)
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?skill=review&"+scope, &sessions)
	if len(sessions) != 1 {
		t.Fatal("parent span time hid nested skill", sessions)
	}
	var summary struct {
		Groups []struct {
			Records int
			Key     string
		}
	}
	getJSON(t, a, "/api/search/summary?skill=review&group=day&"+scope, &summary)
	if len(summary.Groups) != 1 || summary.Groups[0].Records != 1 || summary.Groups[0].Key != "2026-11-14" {
		t.Fatal(summary)
	}
}

func TestClaudeTranscriptUUIDCorrelatesWithoutConfusingProviderMessageID(t *testing.T) {
	a := setup(t)
	save(t, a,
		recorded(t, "import-user", "import", "claude", "root", "gen_ai.user.message", map[string]any{"event.id": "entry-user", "prompt": "hello"}),
		recorded(t, "native-user", "otel", "claude", "root", "claude_code.user_prompt", map[string]any{"message.uuid": "entry-user", "prompt_text": "hello"}),
		recorded(t, "import-reply", "import", "claude", "root", "gen_ai.assistant.message", map[string]any{"event.id": "entry-final", "gen_ai.message.id": "provider-message", "response": "reply"}),
		recorded(t, "native-reply", "otel", "claude", "root", "claude_code.assistant_response", map[string]any{"message.uuid": "entry-final", "request_id": "request-id", "response": "reply"}),
	)
	p := page(t, a, "claude:root", "", 0)
	if p.Count != 2 || len(p.Events) != 2 {
		t.Fatal(p)
	}
	for _, e := range p.Events {
		if len(e.Records) != 2 {
			t.Fatal(e)
		}
	}
}
