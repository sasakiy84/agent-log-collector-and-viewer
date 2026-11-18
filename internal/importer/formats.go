package importer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (p *state) codex(v map[string]any, line int) error {
	t, d := s(v["type"]), m(v["payload"])
	switch t {
	case "session_meta":
		p.session = first(d["id"], d["session_id"])
		p.cwd = s(d["cwd"])
		p.parent = first(d["parent_thread_id"], m(m(d["source"])["subagent"])["parent_thread_id"], m(m(m(d["source"])["subagent"])["thread_spawn"])["parent_thread_id"])
		if p.stamp == "" {
			p.stamp = s(d["timestamp"])
		}
		// Newer linked/forked histories require the history reducer, not a guessed flattening.
		if d["history_base"] != nil {
			return errors.New("linked Codex history_base is not supported; provide a flattened transcript")
		}
	case "turn_context":
		p.model = s(d["model"])
		if p.cwd == "" {
			p.cwd = s(d["cwd"])
		}
	case "response_item":
		typ := s(d["type"])
		switch typ {
		case "message":
			role := s(d["role"])
			if role == "user" || role == "assistant" {
				return p.codexMessage(role, content(d["content"]), s(d["phase"]), false, s(d["id"]))
			}
		case "function_call", "custom_tool_call":
			return p.tool(s(d["call_id"]), s(d["name"]), first(d["arguments"], d["input"]), nil, false, false)
		case "function_call_output", "custom_tool_call_output":
			return p.tool(s(d["call_id"]), "", nil, d["output"], false, true)
		default:
			p.skipped++
		}
	case "event_msg":
		switch s(d["type"]) {
		case "user_message", "agent_message":
			role := "user"
			if s(d["type"]) == "agent_message" {
				role = "assistant"
			}
			return p.codexMessage(role, first(d["message"], d["text"]), s(d["phase"]), true, "")
		case "token_count":
			u := m(m(d["info"])["last_token_usage"])
			if len(u) > 0 {
				fields := map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": u["input_tokens"], "gen_ai.usage.output_tokens": u["output_tokens"], "gen_ai.usage.cache_read.input_tokens": u["cached_input_tokens"], "gen_ai.usage.cache_write.input_tokens": u["cache_write_input_tokens"], "gen_ai.usage.reasoning.output_tokens": u["reasoning_output_tokens"]}
				total := m(m(d["info"])["total_token_usage"])
				if len(total) > 0 {
					fields["agentlog.usage.counter"] = true
					if reported, ok := new(big.Int).SetString(s(total["total_tokens"]), 10); ok {
						input, okI := new(big.Int).SetString(s(total["input_tokens"]), 10)
						output, okO := new(big.Int).SetString(s(total["output_tokens"]), 10)
						if !okI || !okO || reported.Cmp(input.Add(input, output)) != 0 {
							fields["agentlog.usage.counter_incomplete"] = true
						}
					}
					for key, value := range map[string]any{"input_tokens": total["input_tokens"], "output_tokens": total["output_tokens"], "cache_read_tokens": total["cached_input_tokens"], "cache_write_tokens": total["cache_write_input_tokens"], "reasoning_tokens": total["reasoning_output_tokens"]} {
						fields["agentlog.usage.total."+key] = value
					}
				}
				return p.output("gen_ai.client.operation", fields, fmt.Sprint(line))
			}
		default:
			p.skipped++
		}
	default:
		p.skipped++
	}
	return nil
}

// Content and occurrence identify both Codex message projections, including when
// a canonical response_item arrives after its event_msg was already imported.
func (p *state) codexMessage(role, text, phase string, fallback bool, nativeID string) error {
	key := role + "\x00" + text
	counts := p.messageCounts
	if fallback {
		counts = p.fallbackCounts
	}
	counts[key]++
	n := counts[key]
	if fallback && n <= p.canonicalMessages[key] {
		return nil
	}
	fields := map[string]any{"gen_ai.message.id": nativeID}
	if role == "assistant" && phase == "commentary" {
		fields["codex.message.phase"] = phase
	}
	return p.messageWithIDs(role, text, fmt.Sprintf("message:%x:%d", sha256.Sum256([]byte(key)), n), fields)
}

func (p *state) claude(v map[string]any, line int) error {
	if id := s(v["sessionId"]); id != "" {
		p.session = id
	}
	if cwd := s(v["cwd"]); cwd != "" {
		p.cwd = cwd
	}
	p.agent = s(v["agentId"])
	if p.agent == "" && filepath.Base(filepath.Dir(p.file)) == "subagents" {
		base := filepath.Base(p.file)
		if strings.HasPrefix(base, "agent-") {
			p.agent = strings.TrimSuffix(strings.TrimPrefix(base, "agent-"), ".jsonl")
		}
	}
	msg := m(v["message"])
	if model := s(msg["model"]); model != "" {
		p.model = model
	}
	typ := s(v["type"])
	if typ != "user" && typ != "assistant" {
		p.skipped++
		return nil
	}
	id := first(v["uuid"], msg["id"], fmt.Sprint(line))
	if str, ok := msg["content"].(string); ok {
		return p.messageWithIDs(typ, str, id, map[string]any{"event.id": v["uuid"], "gen_ai.message.id": msg["id"]})
	}
	// A text projection is emitted once per message, followed by tool blocks in order.
	if e := p.messageWithIDs(typ, content(msg["content"]), id, map[string]any{"event.id": v["uuid"], "gen_ai.message.id": msg["id"]}); e != nil {
		return e
	}
	for _, b := range a(msg["content"]) {
		block := m(b)
		switch s(block["type"]) {
		case "tool_use":
			if e := p.tool(s(block["id"]), s(block["name"]), block["input"], nil, false, false); e != nil {
				return e
			}
		case "tool_result":
			out := block["content"]
			if text := content(out); text != "" {
				out = text
			}
			if e := p.tool(s(block["tool_use_id"]), "", nil, out, block["is_error"] == true, true); e != nil {
				return e
			}
		case "text":
		default:
			p.skipped++
		}
	}
	if u := m(msg["usage"]); len(u) > 0 {
		input := u["input_tokens"]
		if total, ok := new(big.Int).SetString(s(input), 10); ok {
			for _, key := range []string{"cache_read_input_tokens", "cache_creation_input_tokens"} {
				if count, ok := new(big.Int).SetString(s(u[key]), 10); ok {
					total.Add(total, count)
				}
			}
			input = json.Number(total.String())
		}
		return p.output("gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": input, "gen_ai.usage.output_tokens": u["output_tokens"], "gen_ai.usage.cache_read.input_tokens": u["cache_read_input_tokens"], "gen_ai.usage.cache_write.input_tokens": u["cache_creation_input_tokens"], "gen_ai.response.id": msg["id"], "agentlog.usage.input_includes_cache": true}, id+"|usage")
	}
	return nil
}
func (p *state) copilot(v map[string]any, line int) error {
	d := m(v["data"])
	p.agent = s(v["agentId"])
	typ := s(v["type"])
	id := first(v["id"], fmt.Sprint(line))
	if typ == "session.journal_link" {
		return errors.New("journal link must be handled by the file reader")
	}
	if v["checkpoint"] == true {
		p.skipped++
		return nil
	}
	if typ == "session.start" {
		p.session = s(d["sessionId"])
		p.cwd = s(m(d["context"])["cwd"])
		p.model = s(d["selectedModel"])
		return nil
	}
	if p.session == "" && filepath.Base(p.file) == "events.jsonl" {
		p.session = filepath.Base(filepath.Dir(p.file))
	}
	// agentId is a runtime identity; keep it as attribution rather than inventing a new session.
	switch typ {
	case "user.message":
		return p.messageWithIDs("user", s(d["content"]), id, map[string]any{"event.id": v["id"], "gen_ai.message.id": d["messageId"]})
	case "assistant.message":
		return p.messageWithIDs("assistant", s(d["content"]), id, map[string]any{"event.id": v["id"], "gen_ai.message.id": d["messageId"]})
	case "tool.execution_start":
		return p.tool(s(d["toolCallId"]), s(d["toolName"]), d["arguments"], nil, false, false)
	case "tool.execution_complete":
		out := first(m(d["result"])["content"], m(d["error"])["message"])
		if out == "" {
			out = s(d["result"])
		}
		return p.tool(s(d["toolCallId"]), s(d["toolName"]), nil, out, d["success"] == false, true)
	case "assistant.usage":
		if model := s(d["model"]); model != "" {
			p.model = model
		}
		return p.output("gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": d["inputTokens"], "gen_ai.usage.output_tokens": d["outputTokens"], "gen_ai.usage.cache_read.input_tokens": d["cacheReadTokens"], "gen_ai.usage.cache_write.input_tokens": d["cacheWriteTokens"], "github.copilot.cost": d["cost"]}, id)
	case "subagent.started", "subagent.completed":
		return p.output("gen_ai.agent."+strings.TrimPrefix(typ, "subagent."), map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": d["agentName"], "gen_ai.agent.id": v["agentId"], "parent_agent_id": d["parentId"]}, id)
	case "session.resume":
	default:
		p.skipped++
	}
	return nil
}
func (p *state) journal(ctx context.Context, file string, d map[string]any) error {
	prev := s(d["previousFile"])
	digits := strings.TrimSuffix(strings.TrimPrefix(prev, "events."), ".jsonl")
	if !strings.HasPrefix(prev, "events.") || !strings.HasSuffix(prev, ".jsonl") {
		return errors.New("invalid journal filename")
	}
	if _, e := strconv.ParseUint(digits, 10, 64); e != nil {
		return errors.New("invalid journal filename")
	}
	path := filepath.Join(filepath.Dir(file), prev)
	info, e := os.Lstat(path)
	if e != nil {
		return fmt.Errorf("missing predecessor journal: %w", e)
	}
	if !info.Mode().IsRegular() {
		return errors.New("journal must be a regular file")
	}
	if p.session == "" {
		p.session = s(d["sessionId"])
	}
	return p.parse(ctx, path)
}

// decodeObject keeps integer token counts exact instead of routing them through float64.
func decodeObject(b []byte, v *map[string]any) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
