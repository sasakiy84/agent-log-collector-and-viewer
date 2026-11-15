package app

import (
	"fmt"
	"net/url"
	"testing"
)

func inspected(t *testing.T, a *App, session, id, origin string) TimelineEvent {
	t.Helper()
	var event TimelineEvent
	getJSON(t, a, "/api/event?session="+url.QueryEscape(session)+"&id="+url.QueryEscape(id)+"&origin="+origin, &event)
	return event
}
func TestToolPairAcrossPagesAndReverse(t *testing.T) {
	a := setup(t)
	start := recorded(t, "input", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"gen_ai.tool.call.id": "call-1", "gen_ai.tool.name": "exec_command", "gen_ai.tool.call.arguments": `{"cmd":"go test ./..."}`})
	batch := []Event{start}
	for i := 1; i < 205; i++ {
		e := recorded(t, fmt.Sprint(i), "otel", "codex", "root", "gen_ai.assistant.message", map[string]any{"response": "Working"})
		e.Sequence = i
		batch = append(batch, e)
	}
	result := recorded(t, "output", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "call-1", "gen_ai.tool.name": "exec_command", "gen_ai.tool.call.result": "PASS"})
	result.Sequence = 300
	batch = append(batch, result)
	save(t, a, batch...)
	input := inspected(t, a, "codex:root", start.ID, "")
	if input.ToolPair == nil || input.ToolPair.State != "paired" || input.ToolPair.Counterpart.ID != result.ID || !hasBlock(input.ToolPair.Counterpart.Detail, "Output", "PASS") {
		t.Fatalf("input pair: %+v", input.ToolPair)
	}
	output := inspected(t, a, "codex:root", result.ID, "")
	if output.ToolPair.State != "paired" || output.ToolPair.Counterpart.ID != start.ID || !hasBlock(output.ToolPair.Counterpart.Detail, "Command", "go test ./...") {
		t.Fatalf("output pair: %+v", output.ToolPair)
	}
	if p := page(t, a, "codex:root", "", 0); p.Count != 206 {
		t.Fatal("input/output were incorrectly merged", p.Count)
	}
}
func TestToolPairOriginIsolationAndAmbiguity(t *testing.T) {
	a := setup(t)
	start := recorded(t, "input", "import", "claude", "root", "gen_ai.tool.start", map[string]any{"call_id": "call", "tool_name": "Bash", "arguments": `{"command":"pwd"}`})
	native := recorded(t, "native-output", "otel", "claude", "root", "tool_result", map[string]any{"call_id": "call", "tool_name": "Bash", "output": "/work", "success": false})
	save(t, a, start, native)
	if pair := inspected(t, a, "claude:root", start.ID, "import").ToolPair; pair.State != "missing" {
		t.Fatal("native output leaked into import view", pair)
	}
	if pair := inspected(t, a, "claude:root", start.ID, "").ToolPair; pair.State != "paired" || !pair.Counterpart.Detail.Error {
		t.Fatal(pair)
	}
	imported := recorded(t, "imported-output", "import", "claude", "root", "gen_ai.tool.result", map[string]any{"call_id": "call", "tool_name": "Bash", "output": "/work"})
	save(t, a, imported)
	if pair := inspected(t, a, "claude:root", start.ID, "").ToolPair; pair.State != "paired" || pair.Counterpart.ID != imported.ID {
		t.Fatal("paired import/OTEL became ambiguous", pair)
	}
	reused := recorded(t, "reused", "import", "claude", "root", "gen_ai.tool.start", map[string]any{"call_id": "call", "tool_name": "Bash", "arguments": "another input"})
	save(t, a, reused)
	for _, id := range []string{start.ID, imported.ID} {
		if pair := inspected(t, a, "claude:root", id, "").ToolPair; pair.State != "ambiguous" || pair.Counterpart != nil {
			t.Fatal("reused ID was guessed", pair)
		}
	}
}
func TestToolPairExecutingAgentAndSessionScope(t *testing.T) {
	a := setup(t)
	start := recorded(t, "a", "otel", "copilot", "root", "tool.execution_start", map[string]any{"tool_name": "shell", "call_id": "same", "agent_id": "a"})
	otherOwner := recorded(t, "b", "otel", "copilot", "root", "tool.execution_complete", map[string]any{"tool_name": "shell", "call_id": "same", "agent_id": "b", "output": "wrong owner"})
	otherSession := recorded(t, "c", "otel", "copilot", "other", "tool.execution_complete", map[string]any{"tool_name": "shell", "call_id": "same", "agent_id": "a", "output": "wrong session"})
	save(t, a, start, otherOwner, otherSession)
	if pair := inspected(t, a, "copilot:root", start.ID, "").ToolPair; pair.State != "missing" {
		t.Fatal(pair)
	}
	result := recorded(t, "d", "otel", "copilot", "root", "tool.execution_complete", map[string]any{"tool_name": "shell", "call_id": "same", "agent_id": "a", "output": "right"})
	save(t, a, result)
	if pair := inspected(t, a, "copilot:root", start.ID, "").ToolPair; pair.State != "paired" || pair.Counterpart.ID != result.ID {
		t.Fatal(pair)
	}
}
func TestCodexInternalTracesOptInAndReconciliation(t *testing.T) {
	a := setup(t)
	trace := recorded(t, "trace", "otel", "codex", "unused", "fs.read_file", map[string]any{})
	trace.Session = "trace:codex:abc"
	trace.Trace = "abc"
	trace.ParentSpan = "root"
	unassigned := recorded(t, "unassigned", "otel", "codex", "unused", "startup", map[string]any{})
	unassigned.Session = "unassigned:codex"
	real := recorded(t, "real", "import", "codex", "real", "gen_ai.user.message", map[string]any{"prompt": "Real session without cwd"})
	save(t, a, trace, unassigned, real)
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].ID != "codex:real" {
		t.Fatal(sessions)
	}
	getJSON(t, a, "/api/sessions?diagnostics=1", &sessions)
	if len(sessions) != 3 {
		t.Fatal("internal records became inaccessible", sessions)
	}
	var folders folderPage
	getJSON(t, a, "/api/folders", &folders)
	if folders.Total != 1 || folders.Folders[0].Count != 1 {
		t.Fatal(folders)
	}
	if p := page(t, a, trace.Session, "", 0); p.Count != 1 {
		t.Fatal("original trace was removed")
	}
	owner := recorded(t, "owner", "otel", "codex", "real", "codex.user_prompt", map[string]any{"prompt": "Owner"})
	owner.Trace = "abc"
	owner.Span = "root"
	save(t, a, owner)
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].Count != 3 {
		t.Fatal("trace did not join its real conversation", sessions)
	}
}
