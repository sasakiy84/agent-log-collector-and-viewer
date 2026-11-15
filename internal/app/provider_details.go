package app

import (
	"strconv"
	"strings"
	"time"
)

// Decode content-bearing values, not arbitrary identifiers or JSON records.
// Known tool envelopes become readable text; unknown structured results retain
// their JSON representation rather than losing fields silently.
func toolContent(v any) any { return toolContentDepth(v, 0) }
func toolContentDepth(v any, depth int) any {
	if depth >= 16 {
		return v
	}
	d := decoded(v)
	if m, ok := d.(map[string]any); ok {
		if len(m) == 1 {
			for _, key := range []string{"content", "text", "output"} {
				if value, exists := m[key]; exists {
					return toolContentDepth(value, depth+1)
				}
			}
		}
	}
	if blocks, ok := d.([]any); ok {
		texts := []string{}
		for _, value := range blocks {
			m := obj(value)
			if text, ok := firstValue(m, "text", "content").(string); ok && (m["type"] == "text" || m["type"] == "input_text" || m["type"] == "output_text" || m["type"] == nil) {
				texts = append(texts, text)
			} else {
				return d
			}
		}
		return strings.Join(texts, "\n")
	}
	return v
}

func providerDetails(e Event, p *Detail, r map[string]any) {
	a := p.Attributes
	get := func(keys ...string) any { return firstValue(a, keys...) }
	name := eventSuffix(p.Name)
	if e.Source == "claude" {
		switch name {
		case "skill_activated":
			p.Kind = "skill"
			p.Title = firstString(str(a["skill.name"]), "Skill activated")
			p.field("Trigger", a["invocation_trigger"])
			p.field("Scope", a["skill.source"])
			p.field("Plugin", a["plugin.name"])
		case "api_error":
			p.Kind = "model"
			p.Title = "Model call failed"
		case "api_refusal":
			p.Kind = "model"
			p.Title = "Model refusal"
			p.field("Category", a["category"])
		case "tool_decision":
			p.Title = firstString(str(get("tool_name", "tool_name_safe")), "Tool") + " permission"
			p.field("Decision", get("decision", "decision_type"))
			p.field("Decision source", get("source", "decision_source"))
		case "subagent_completed":
			p.Kind = "agent"
			p.Title = firstString(str(a["agent_type"]), "Subagent") + " completed"
			p.field("Tool calls", a["total_tool_uses"])
			p.field("Final request tokens", a["total_tokens"])
			p.field("Background", a["is_async"])
			p.field("Final model", a["final_model"])
		}
		p.field("Skill", get("skill_name", "skill.name"))
		p.field("Agent type", get("subagent_type", "agent_type"))
		if p.Kind == "tool" {
			p.field("Decision source", a["decision_source"])
			p.field("MCP server", get("mcp_server_name"))
			p.field("MCP tool", get("mcp_tool_name"))
		}
	}
	if e.Source == "copilot" {
		for i := range p.Blocks {
			if p.Blocks[i].Label == "user" || p.Blocks[i].Label == "Request" {
				p.Blocks[i].Text = userPromptText(p.Blocks[i].Text, "copilot")
			}
		}
		for index, value := range arr(r["events"]) {
			event := obj(value)
			if event["name"] != "github.copilot.skill.invoked" {
				continue
			}
			at := decodedAttributes(event["attributes"])
			name := skillString(firstValue(at, "gen_ai.skill.name", "github.copilot.skill.name"), 256)
			stamp := ""
			if n, err := strconv.ParseInt(str(event["timeUnixNano"]), 10, 64); err == nil && n > 0 {
				stamp = time.Unix(0, n).UTC().Format("2006-01-02T15:04:05.000000000Z")
			}
			if name != "" {
				p.SkillFacts = append(p.SkillFacts, SkillEvidence{Name: name, Plugin: skillString(firstValue(at, "gen_ai.skill.plugin.name", "github.copilot.skill.plugin_name"), 256), Path: skillString(firstValue(at, "gen_ai.skill.source.uri", "github.copilot.skill.path"), 32768), Scope: skillString(at["github.copilot.skill.source"], 64), Mode: skillString(at["github.copilot.skill.invocation_trigger"], 64), Evidence: "reported", Time: stamp, Instance: "span-event:" + strconv.Itoa(index)})
			}
		}

		if p.Turn == "" {
			p.Turn = str(get("github.copilot.interaction_id"))
		}
		if a["gen_ai.operation.name"] == "invoke_agent" {
			p.sessionPrompt = e.ParentSpan == "" || present(a["server.address"])
			p.Title = firstString(str(a["gen_ai.agent.name"]), "Agent turn")
			for _, v := range arr(r["events"]) {
				ev := obj(v)
				at := decodedAttributes(ev["attributes"])
				if ev["name"] == "github.copilot.user.message" && p.Turn == "" {
					p.Turn = str(at["github.copilot.user.message.interaction_id"])
				}
			}
			p.field("Model calls", a["github.copilot.turn_count"])
		}
		p.field("Request purpose", a["github.copilot.request.type"])
	}
	p.field("Attempts", get("attempt"))
	p.field("Error category", get("error_class"))
	if p.Kind == "tool" {
		// Tool output span events carry the content in Claude's current exporter.
		for _, v := range arr(r["events"]) {
			ev := obj(v)
			if ev["name"] != "tool.output" {
				continue
			}
			at := decodedAttributes(ev["attributes"])
			p.block("Output", toolContent(firstValue(at, "output", "content", "tool.output")), true)
			p.block("Diff", at["diff"], true)
			if at["content_truncated"] == true || at["output_truncated"] == true || at["diff_truncated"] == true {
				p.field("Truncated", true)
			}
		}
	}
}
