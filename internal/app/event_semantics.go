package app

import "strings"

// Guardian observations describe the review machinery, not execution of the
// tool named in their context. They must never acquire tool-call identities.
func guardianInternal(name string, a map[string]any) bool {
	return strings.HasPrefix(name, "guardian_") ||
		strings.HasPrefix(str(a["code.module.name"]), "codex_guardian_v2::") ||
		strings.HasPrefix(str(a["target"]), "codex_guardian_v2::") ||
		strings.HasPrefix(str(a["code.file.path"]), "ext/guardian-v2/")
}

func toolBodyPresent(d Detail) bool {
	for _, block := range d.Blocks {
		switch block.Label {
		case "Input", "Output", "Command", "File", "Diff":
			text := strings.TrimSpace(block.Text)
			if text != "" && text != "Content not exported" && text != "Tool content not exported" {
				return true
			}
		}
	}
	return false
}

func codexToolMetadata(e Event, d Detail) bool {
	return e.Source == "codex" && e.Signal == "traces" && e.Origin != "import" &&
		str(d.Attributes["agentlog.origin"]) != "import" && d.Kind == "tool" && !toolBodyPresent(d)
}

// A contextual tool_name and a completed span do not establish a tool result.
// Explicit tool event names/operations retain their input/output semantics.
func codexToolTiming(e Event, d Detail) bool {
	return codexToolMetadata(e, d) && toolPhase(d.Name, "", nil) == "" &&
		str(d.Attributes["gen_ai.operation.name"]) != "execute_tool"
}

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
	if e.Source == "codex" && guardianInternal(d.Name, d.Attributes) {
		return true
	}
	if codexToolMetadata(e, d) {
		return true
	}
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
func detailConversation(d Detail, source string) bool {
	// Child model spans often repeat the complete input history. The root
	// Copilot agent envelope is authoritative; delegated tasks are processing.
	if d.Diagnostic || source == "copilot" && d.Kind == "agent" && !d.SessionPrompt {
		return false
	}
	return len(conversationBlocks(d, source)) > 0
}

func conversationBlocks(d Detail, source string) []Block {
	out := []Block{}
	if d.Kind != "user" && d.Kind != "assistant" && d.Kind != "model" && d.Kind != "agent" {
		return out
	}
	for _, b := range d.Blocks {
		switch b.Label {
		case "Request", "user":
			b.Text = userPromptText(b.Text, source)
			if source == "codex" && codexAutomaticPrompt(b.Text, d.Model) {
				continue
			}
		case "Response", "assistant", "Input", "Output":
		default:
			continue
		}
		if b.Text != "" {
			out = append(out, b)
		}
	}
	return out
}
