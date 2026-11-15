package app

import "strings"

func eventSuffix(name string) string {
	for _, prefix := range []string{"codex.", "claude_code.", "github.copilot.", "copilot."} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

func isToolEvent(name string, a map[string]any) bool {
	if present(firstValue(a, "gen_ai.tool.name", "tool_name", "tool_name_safe")) {
		return true
	}
	switch eventSuffix(name) {
	case "gen_ai.tool.start", "gen_ai.tool.result", "tool_call", "tool_use", "tool_result", "tool_decision", "tool.execution_start", "tool.execution_complete", "execute_tool":
		return true
	}
	return false
}

func isModelEvent(name string, a map[string]any) bool {
	switch eventSuffix(name) {
	case "gen_ai.client.operation", "api_request", "llm_request", "sse_event", "websocket_request", "run_sampling_request", "chat":
		return true
	}
	return false
}

func diagnosticDetail(e Event, d Detail) bool {
	if d.Kind == "metric" {
		return true
	}
	if e.Origin != "import" && (e.Source == "claude" || e.Source == "copilot") {
		if d.Kind == "model" && ((e.Source == "copilot" && e.Signal == "traces" && e.ParentSpan != "") || (e.Source == "claude" && len(d.Blocks) == 0)) {
			return true
		}
		if e.Source == "claude" && (eventSuffix(d.Name) == "tool_decision" || d.Kind == "event") {
			return true
		}
	}
	native := e.Origin != "import" && str(d.Attributes["agentlog.origin"]) != "import"
	if e.Source != "codex" || !native {
		return false
	}
	if d.Name == "codex.tool_decision" || d.Name == "codex.sandbox_outcome" {
		return true
	}
	if d.Kind == "event" {
		return true
	}
	if d.Kind == "model" {
		return len(d.Blocks) == 0
	}
	return false
}

func detailActivity(d Detail) bool { return !d.Diagnostic || d.Error }

// Preserve conversations exported as message blocks inside a model/agent event.
func detailConversation(d Detail) bool {
	if d.Kind == "user" || d.Kind == "assistant" {
		return true
	}
	if d.Kind == "model" || d.Kind == "agent" {
		for _, b := range d.Blocks {
			switch b.Label {
			case "user", "assistant", "Input", "Output":
				return true
			}
		}
	}
	return false
}
