package app

import "strings"

// Full-text search follows the exported conversation and tool content. Keep
// message roles and phases before any display formatting flattens them.
func searchContent(event Event, doc document, detail Detail) []any {
	a := detail.Attributes
	var content []any
	switch detail.Kind {
	case "user":
		request := str(firstValue(a, "prompt_text", "prompt", "user_prompt"))
		if request == "" {
			request = searchMessageText(a["gen_ai.input.messages"], "user", true)
		}
		content = append(content, humanSearchPrompt(request, event.Source, detail.Model))
	case "assistant", "agent", "model":
		if detail.Kind == "model" || detail.SessionPrompt {
			request := str(firstValue(a, "prompt_text", "prompt", "user_prompt"))
			if request == "" && detail.SessionPrompt && !detail.Diagnostic {
				request = searchMessageText(a["gen_ai.input.messages"], "user", false)
			}
			content = append(content, humanSearchPrompt(request, event.Source, detail.Model))
		}
		eventKind := str(a["event.kind"])
		if !intermediateSearchMessage(a) && !strings.HasSuffix(eventKind, ".delta") && eventKind != "response.in_progress" &&
			(detail.Kind == "assistant" || !detail.Diagnostic && (detail.Kind != "agent" || event.Source != "copilot" || detail.SessionPrompt)) {
			content = append(content,
				firstValue(a, "response", "response_text", "assistant_response", "text", "response.model_output"),
				searchMessageText(a["gen_ai.output.messages"], "assistant", detail.Kind == "assistant"))
		}
	case "tool":
		content = append(content,
			firstValue(a, "gen_ai.tool.name", "tool_name", "tool_name_safe"), a["file_path"], a["full_command"], a["tool_parameters"], a["diff"],
			firstValue(a, "gen_ai.tool.call.arguments", "tool_input", "arguments"),
			firstValue(a, "gen_ai.tool.call.result", "tool_result", "output", "result", "tool.output"))
	}
	if detail.Error {
		for _, block := range detail.Blocks {
			if block.Label == "Error" {
				content = append(content, block.Text)
			}
		}
		content = append(content, anyValue(doc.Record["body"]), obj(doc.Record["status"])["message"])
	}
	for _, key := range []string{"error", "error.message", "error.type", "error_type", "exception.message", "exception.type", "exception.stacktrace"} {
		content = append(content, a[key])
	}
	for _, raw := range arr(doc.Record["events"]) {
		spanEvent := obj(raw)
		attributes := decodedAttributes(spanEvent["attributes"])
		content = append(content, attributes["exception.message"], attributes["exception.type"], attributes["exception.stacktrace"])
		if detail.Kind == "tool" && spanEvent["name"] == "tool.output" {
			content = append(content, firstValue(attributes, "output", "content", "tool.output"), attributes["diff"])
		}
	}
	return content
}

func humanSearchPrompt(text, source string, model any) string {
	text = userPromptText(text, source)
	if source == "codex" && codexAutomaticPrompt(text, model) {
		return ""
	}
	return text
}

func intermediateSearchMessage(a map[string]any) bool {
	switch firstValue(a, "gen_ai.message.phase", "message.phase", "phase") {
	case "commentary", "partial_answer":
		return true
	}
	return false
}

// A model envelope can repeat the entire input history. Use the current
// user message and the eligible assistant output in a message array.
func searchMessageText(value any, role string, allowUnlabelled bool) string {
	value = decoded(value)
	messages, ok := value.([]any)
	if !ok {
		if allowUnlabelled {
			text, _ := value.(string)
			return text
		}
		return ""
	}
	var selected []string
	for _, raw := range messages {
		m := obj(raw)
		messageRole := str(m["role"])
		if messageRole != role && !(messageRole == "" && allowUnlabelled) {
			continue
		}
		if role == "assistant" && intermediateSearchMessage(m) {
			continue
		}
		if text := searchMessageParts(firstValue(m, "parts", "content")); text != "" {
			selected = append(selected, text)
		}
	}
	if role == "user" && len(selected) > 0 {
		return selected[len(selected)-1]
	}
	return strings.Join(selected, "\n")
}

func searchMessageParts(parts any) string {
	if text, ok := parts.(string); ok {
		return text
	}
	var texts []string
	for _, raw := range arr(parts) {
		part := obj(raw)
		switch part["type"] {
		case nil, "", "text", "input_text", "output_text":
			if body, ok := firstValue(part, "content", "text").(string); ok && body != "" {
				texts = append(texts, body)
			}
		}
	}
	return strings.Join(texts, "\n")
}
