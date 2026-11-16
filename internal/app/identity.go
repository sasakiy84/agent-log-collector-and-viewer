package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type correlationKey struct {
	Namespace string
	Key       string
}

func identityKey(agent, phase, namespace, id string) string {
	b, _ := json.Marshal([]string{agent, phase, namespace, id})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// correlationKeys only uses identifiers with known semantics. Text, timestamps,
// trace/span IDs and response IDs are deliberately not matching heuristics.
func correlationKeys(e Event, p Detail, d document) []correlationKey {
	if e.Signal == "metrics" || (e.Source != "codex" && e.Source != "claude" && e.Source != "copilot") {
		return nil
	}
	agent, ok := agentIdentity(e.Source, p.Attributes)
	if !ok {
		return nil
	}
	phase := p.Kind
	switch p.Kind {
	case "user", "assistant":
	case "tool":
		phase = toolPhase(p.Name, e.Signal, d.Record)
		if phase == "" {
			return nil
		}
	default:
		return nil
	}
	at := p.Attributes
	keys := map[string]correlationKey{}
	add := func(namespace string, names ...string) {
		for _, name := range names {
			if id, ok := at[name].(string); ok && id != "" {
				key := identityKey(agent, phase, namespace, id)
				keys[key] = correlationKey{namespace, key}
			}
		}
	}
	if p.Kind == "tool" {
		add("tool.call", "gen_ai.tool.call.id", "tool_call_id", "call_id", "tool_use_id")
	} else {
		add("event", "event.id", "event_id")
		add("message", "gen_ai.message.id", "message.id", "message_id")
		if e.Source == "claude" {
			add("event", "message.uuid")
		}
		if p.Kind == "user" {
			add("prompt", "prompt.id", "prompt_id")
		}
	}
	out := make([]correlationKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
func toolPhase(name, signal string, r map[string]any) string {
	if guardianInternal(name, decodedAttributes(r["attributes"])) {
		return ""
	}
	for _, prefix := range []string{"codex.", "claude_code.", "copilot.", "github.copilot."} {
		name = strings.TrimPrefix(name, prefix)
	}
	switch name {
	case "gen_ai.tool.start", "tool_call", "tool_use", "tool.execution_start":
		return "tool.start"
	case "gen_ai.tool.result", "tool_result", "tool.execution_complete":
		return "tool.result"
	}
	if signal == "traces" && present(r["startTimeUnixNano"]) && present(r["endTimeUnixNano"]) && str(r["endTimeUnixNano"]) != "0" {
		return "tool.result"
	}
	return ""
}

// For message/tool events these attributes identify the executing agent. An
// invoke_agent span describes a target agent, so it is not a duplicate candidate.
func agentIdentity(source string, at map[string]any) (string, bool) {
	if at["gen_ai.operation.name"] == "invoke_agent" {
		return "", false
	}
	id := ""
	keys := []string{"agent_id", "gen_ai.agent.id"}
	if source == "copilot" {
		keys = []string{"agent_id"}
	}
	for _, key := range keys {
		value := at[key]
		if !present(value) {
			continue
		}
		next, ok := value.(string)
		if !ok || id != "" && id != next {
			return "", false
		}
		id = next
	}
	return id, true
}
