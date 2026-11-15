package app

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type Block struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	Code  bool   `json:"code"`
}
type Detail struct {
	Diagnostic bool           `json:"diagnostic,omitempty"`
	Turn       string         `json:"turn,omitempty"`
	ToolPhase  string         `json:"tool_phase,omitempty"`
	Conflicts  []Conflict     `json:"conflicts,omitempty"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title"`
	Blocks     []Block        `json:"blocks"`
	Fields     [][2]string    `json:"fields"`
	Error      bool           `json:"error"`
	Model      any            `json:"model,omitempty"`
	Host       any            `json:"host,omitempty"`
	Cwd        any            `json:"cwd,omitempty"`
	Project    any            `json:"project,omitempty"`
	Tags       any            `json:"tags,omitempty"`
	Attributes map[string]any `json:"attributes"`
}

func (p *Detail) field(label string, v any) {
	if present(v) {
		p.Fields = append(p.Fields, [2]string{label, display(v)})
	}
}
func (p *Detail) block(label string, v any, code bool) {
	if present(v) {
		if code {
			switch structured := decoded(v).(type) {
			case map[string]any, []any:
				v = structured
			}
		}
		p.Blocks = append(p.Blocks, Block{label, display(v), code})
	}
}
func (p *Detail) messages(label string, v any) {
	v = decoded(v)
	messages, ok := v.([]any)
	if !ok {
		p.block(label, v, false)
		return
	}
	for _, v := range messages {
		m := obj(v)
		role := str(m["role"])
		parts := m["parts"]
		if parts == nil {
			parts = m["content"]
		}
		content := parts
		if values, ok := parts.([]any); ok {
			texts := []string{}
			for _, v := range values {
				part := obj(v)
				text := part["content"]
				if text == nil {
					text = part["text"]
				}
				if text == nil && part["type"] == "tool_call" {
					text = part
				}
				if present(text) {
					texts = append(texts, display(text))
				}
			}
			content = strings.Join(texts, "\n")
		}
		if p.Kind == "user" {
			role = "Request"
		} else if p.Kind == "assistant" {
			role = "Response"
		} else if role == "" {
			role = label
		}
		p.block(role, content, false)
	}
}

// describe is the single display decoder for imported and native OTLP records.
// Matching below uses the same kind classification, never arbitrary chat context.
func describe(e Event, d document) Detail {
	a, r := d.attributes(), d.Record
	get := func(keys ...string) any { return firstValue(a, keys...) }
	name := str(get("event.name"))
	if name == "" {
		name = str(r["eventName"])
	}
	if name == "" {
		name = str(r["name"])
	}
	if name == "" {
		name = e.Name
	}
	p := Detail{Name: name, Kind: "event", Title: name, Blocks: []Block{}, Fields: [][2]string{}, Attributes: a}
	if p.Title == "" {
		p.Title = "Event"
	}
	p.Model = get("gen_ai.response.model", "gen_ai.request.model", "model")
	p.Host = get("host.name")
	p.Cwd = get("cwd", "working_directory", "workspace.path")
	p.Project = get("service.namespace", "project.name")
	p.Tags = get("project.tags")
	p.Turn = str(get("turn.id", "turn_id", "root.turn.id"))
	op := str(get("gen_ai.operation.name"))
	switch {
	case e.Source == "codex" && name == "codex.skill_invocation":
		p.Kind = "skill"
		p.Title = firstString(skillString(a["skill.name"], 256), "Skill")
		p.field("Invocation", get("skill.invocation_type"))
		p.field("Scope", get("skill.scope"))
		p.field("Plugin", get("skill.plugin_id"))
	case name == "gen_ai.user.message" || strings.HasSuffix(name, "user_prompt") || strings.HasSuffix(name, "interaction"):
		p.Kind = "user"
		p.Title = "User"
		p.block("Request", get("prompt_text", "prompt", "user_prompt"), false)
	case name == "gen_ai.assistant.message" || eventSuffix(name) == "assistant_response" || e.Source == "codex" && name == "codex.agent_response":
		p.Kind = "assistant"
		p.Title = "Assistant"
		p.block("Response", get("response", "response_text", "assistant_response", "text"), false)
	case isToolEvent(name, a) || op == "execute_tool":
		p.Kind = "tool"
		p.Title = str(get("gen_ai.tool.name", "tool_name", "tool_name_safe"))
		if p.Title == "" {
			p.Title = name
		}
		input := decoded(get("gen_ai.tool.call.arguments", "tool_input", "arguments"))
		parameters := decoded(get("tool_parameters"))
		in, params := obj(input), obj(parameters)
		command := firstValue(a, "full_command")
		if !present(command) {
			command = firstValue(params, "full_command", "command")
		}
		if !present(command) {
			command = firstValue(in, "command", "cmd")
		}
		p.block("Command", command, true)
		file := firstValue(in, "file_path", "path")
		if present(file) {
			p.block("File", file, true)
			p.field("Offset", in["offset"])
			p.field("Limit", in["limit"])
		} else if input != nil && !present(firstValue(in, "command", "cmd")) {
			p.block("Input", input, true)
		} else if parameters != nil && len(p.Blocks) == 0 {
			p.block("Input", parameters, true)
		}
		p.block("Output", get("gen_ai.tool.call.result", "tool_result", "output", "result", "tool.output"), true)
		if present(get("file_path")) && !present(file) {
			p.block("File", get("file_path"), true)
		}
		p.field("Truncated", get("output_truncated"))
		p.field("Decision", get("decision"))
		p.field("Exit code", get("exit_code"))
	case isModelEvent(name, a) || op == "chat":
		p.Kind = "model"
		p.Title = "Model request"
		if e.Source == "codex" && str(a["agentlog.origin"]) == "import" && name == "gen_ai.client.operation" && present(a["agentlog.import.id"]) {
			p.Title = "Token usage"
		}
	case op == "invoke_agent":
		p.Kind = "agent"
		p.Title = str(get("gen_ai.agent.name"))
		if p.Title == "" {
			p.Title = "Agent"
		}
	}
	if p.Kind == "tool" {
		p.ToolPhase = toolPhase(name, e.Signal, r)
	}
	p.messages("Input", get("gen_ai.input.messages"))
	p.messages("Output", get("gen_ai.output.messages"))
	p.block("Response", get("response.model_output"), false)
	if (p.Kind == "user" || p.Kind == "assistant") && len(p.Blocks) == 0 {
		label := "Request"
		if p.Kind == "assistant" {
			label = "Response"
		}
		p.block(label, "Content not exported", false)
	}
	if e.Signal == "metrics" {
		p.Kind = "metric"
		p.Title = firstString(str(r["name"]), name)
		p.field("Value", firstValue(r, "asInt", "asDouble"))
		p.field("Unit", r["unit"])
		p.field("Count", r["count"])
		p.field("Sum", r["sum"])
		p.field("Temporality", map[string]string{"1": "Delta", "2": "Cumulative", "AGGREGATION_TEMPORALITY_DELTA": "Delta", "AGGREGATION_TEMPORALITY_CUMULATIVE": "Cumulative"}[str(r["aggregationTemporality"])])
		if r["bucketCounts"] != nil {
			p.block("Buckets", map[string]any{"bounds": r["explicitBounds"], "counts": r["bucketCounts"]}, true)
		}
	}
	if len(p.Blocks) == 0 && p.Kind == "event" {
		body := anyValue(r["body"])
		if present(body) {
			if display(body) == name {
				p.Title = "Log"
			}
			p.block("Message", body, false)
		}
	}
	p.field("Model", p.Model)
	for _, f := range []struct {
		label string
		keys  []string
	}{
		{"Input tokens", []string{"gen_ai.usage.input_tokens", "input_tokens", "input_token_count"}},
		{"Output tokens", []string{"gen_ai.usage.output_tokens", "output_tokens", "output_token_count"}},
		{"Cache read", []string{"gen_ai.usage.cache_read.input_tokens", "cache_read_tokens", "cached_token_count"}},
		{"Cache write", []string{"gen_ai.usage.cache_write.input_tokens", "cache_creation_tokens", "cache_write_token_count"}},
		{"Reasoning", []string{"gen_ai.usage.reasoning.output_tokens", "reasoning_token_count"}},
		{"Cost (USD)", []string{"cost_usd"}}, {"Billing multiplier", []string{"github.copilot.cost"}}, {"Nano AIU", []string{"github.copilot.nano_aiu"}},
	} {
		p.field(f.label, get(f.keys...))
	}
	ms := get("duration_ms", "interaction.duration_ms", "tool_duration_ms")
	if ms == nil && r["startTimeUnixNano"] != nil && r["endTimeUnixNano"] != nil {
		start, ok1 := new(big.Int).SetString(str(r["startTimeUnixNano"]), 10)
		end, ok2 := new(big.Int).SetString(str(r["endTimeUnixNano"]), 10)
		if ok1 && ok2 {
			value, _ := new(big.Float).SetInt(new(big.Int).Sub(end, start)).Float64()
			ms = value / 1e6
		}
	}
	if ms != nil {
		n, err := strconv.ParseFloat(str(ms), 64)
		if err == nil {
			if n >= 1000 {
				p.field("Duration", fmt.Sprintf("%.2f s", n/1000))
			} else {
				p.field("Duration", fmt.Sprintf("%.0f ms", n))
			}
		}
	}
	if success := get("success"); success != nil {
		result := "Failed"
		if str(success) == "true" {
			result = "Succeeded"
		}
		p.field("Result", result)
	}
	p.field("Finish", get("stop_reason", "gen_ai.response.finish_reasons"))
	p.field("Status", get("status_code", "http.response.status_code"))
	var failure any
	for _, key := range []string{"error", "error.message", "error.type", "error_type"} {
		if v := a[key]; present(v) && v != false {
			failure = v
			break
		}
	}
	status := str(obj(r["status"])["code"])
	statusError := status == "2" || status == "STATUS_CODE_ERROR"
	if !present(failure) && statusError {
		failure = obj(r["status"])["message"]
	}
	p.Error = present(failure) || str(get("success")) == "false" || statusError || containsAny(str(r["severityText"]), "ERROR", "FATAL")
	if p.Error {
		if !present(failure) {
			failure = "Request failed"
		}
		p.block("Error", failure, false)
	}
	for _, v := range arr(r["events"]) {
		event := obj(v)
		at := decodedAttributes(event["attributes"])
		p.block(str(event["name"]), firstValue(at, "tool.output", "exception.message", "output", "content"), false)
	}
	if p.Kind == "tool" && len(p.Blocks) == 0 {
		p.block("Details", "Tool content not exported", false)
	}
	p.Diagnostic = diagnosticDetail(e, p)
	return p
}
func containsAny(s string, values ...string) bool {
	for _, v := range values {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}
func firstString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
