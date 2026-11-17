package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Tool arguments are decoded from original records, never from clipped cards.
func toolSkillReads(name string, input map[string]any, cwd string, script bool) []skillRead {
	name = strings.ToLower(name)
	leaf := name[strings.LastIndex(name, ".")+1:]
	if script && (name == "functions.exec" || name == "exec") {
		if code, ok := input["code"].(string); ok {
			return scriptSkillReads(code, cwd)
		}
	}
	switch leaf {
	case "skill":
		if name := skillString(firstValue(input, "skill", "skill_name", "name"), 256); name != "" {
			return []skillRead{{SkillEvidence: SkillEvidence{Name: name, Evidence: "pending"}, Certain: true}}
		}
	case "exec_command", "shell", "bash", "run_shell_command", "run_in_terminal", "terminal":
		command := firstValue(input, "cmd", "command", "full_command", "bash_command")
		if words, ok := command.([]any); ok {
			if len(words) == 3 && (path.Base(str(words[0])) == "bash" || path.Base(str(words[0])) == "sh") && (words[1] == "-lc" || words[1] == "-c") {
				command = words[2]
			} else {
				return nil
			}
		}
		if dir := skillString(firstValue(input, "workdir", "cwd"), 32768); dir != "" {
			cwd = resolveSkillPath(cwd, dir)
		}
		if command, ok := command.(string); ok {
			return shellSkillReads(command, cwd, 0)
		}
	case "read", "read_file", "read_text", "get_file_contents", "view_file", "view":
		f := skillFromPath(resolveSkillPath(cwd, skillString(firstValue(input, "file_path", "path", "filePath"), 32768)))
		if f.Name != "" {
			return []skillRead{{SkillEvidence: f, Certain: true}}
		}
	}
	if name == "skills.read" || strings.HasSuffix(name, "skills__read") {
		resource := skillString(firstValue(input, "resource"), 32768)
		if resource == "" {
			resource = strings.TrimRight(skillString(input["package"], 32768), "/") + "/SKILL.md"
		}
		f := skillFromPath(resource)
		// Opaque package IDs and local aliases do not identify a skill name.
		if f.Name != "" && strings.Contains(resource, "/") && !strings.HasPrefix(f.Name, "plugin_connector_") {
			return []skillRead{{SkillEvidence: f, Certain: true}}
		}
	}
	return nil
}

func eventSkillReads(d Detail) []skillRead {
	if d.Kind != "tool" {
		return nil
	}
	input := decoded(firstValue(d.Attributes, "gen_ai.tool.call.arguments", "tool_input", "arguments", "tool_parameters"))
	params := obj(input)
	if code, ok := input.(string); ok && (d.Title == "functions.exec" || d.Title == "exec") {
		params = map[string]any{"code": code}
	}
	if len(params) == 0 {
		params = map[string]any{}
		for _, b := range d.Blocks {
			switch b.Label {
			case "Command":
				params["command"] = b.Text
			case "File":
				params["path"] = b.Text
			case "Input":
				if d.Title == "functions.exec" || d.Title == "exec" {
					params["code"] = b.Text
				}
			}
		}
	}
	// Some provider spans export the path or command directly as attributes.
	for _, k := range []string{"file_path", "full_command", "skill_name"} {
		if v := d.Attributes[k]; present(v) {
			params[k] = v
		}
	}
	reads := toolSkillReads(d.Title, params, str(d.Cwd), true)
	// A single outer tool call can establish at most one read per skill.
	unique := map[string]int{}
	out := []skillRead{}
	for _, r := range reads {
		key := r.Name + "\x00" + r.Plugin
		if i, ok := unique[key]; ok {
			out[i].Certain = out[i].Certain || r.Certain
			continue
		}
		if len(out) == 16 {
			break
		}
		unique[key] = len(out)
		out = append(out, r)
	}
	return out
}

// Read status is inferred only from protocol metadata, or a known executor
// envelope before its output section. Arbitrary returned file text is not status.
var skillExit = regexp.MustCompile(`(?m)^Process exited with code (-?[0-9]+)\r?$`)

func skillResultStatus(d Detail, executor bool) string {
	if d.Error || str(d.Attributes["is_error"]) == "true" {
		return "failed"
	}
	if v := d.Attributes["exit_code"]; v != nil {
		if str(v) == "0" {
			return "read"
		}
		return "failed"
	}
	for _, b := range d.Blocks {
		if b.Label != "Output" || !executor {
			continue
		}
		if status := skillStructuredStatus(decoded(b.Text)); status != "" {
			return status
		}
		header, _, _ := strings.Cut(b.Text, "\nFinal output:")
		// Codex exec envelopes precede Final output; don't inspect printed content.
		if strings.HasPrefix(header, "Chunk ID:") || strings.HasPrefix(header, "Wall time:") {
			if m := skillExit.FindStringSubmatch(header); len(m) == 2 {
				if m[1] == "0" {
					return "read"
				}
				return "failed"
			}
		}
	}
	if v := d.Attributes["success"]; v != nil {
		if str(v) == "true" {
			return "read"
		}
		return "failed"
	}
	return "pending"
}
func skillStructuredStatus(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if _, ok := m["contents"].(string); ok && skillFromPath(str(m["resource"])).Name != "" {
		return "read"
	}
	if v, ok := m["exit_code"]; ok && v != nil {
		if str(v) == "0" {
			return "read"
		}
		return "failed"
	}
	if v, ok := m["isError"].(bool); ok {
		if v {
			return "failed"
		}
		return "read"
	}
	return ""
}
func skillReadStatus(input Detail, result Detail, r skillRead) string {
	leaf := strings.ToLower(input.Title)
	leaf = leaf[strings.LastIndex(leaf, ".")+1:]
	executor := false
	switch leaf {
	case "exec_command", "shell", "bash", "run_shell_command", "run_in_terminal", "terminal":
		executor = true
	}
	status := skillResultStatus(result, executor)
	if !r.Certain {
		return "pending"
	}
	if r.Nested {
		// The outer script's exit status doesn't establish an inner tool's outcome.
		// A single explicitly returned executor result can; multiple results require
		// per-call identity, and otherwise stay unconfirmed.
		for _, b := range result.Blocks {
			if b.Label != "Output" {
				continue
			}
			if strings.HasPrefix(b.Text, "Script completed\n") {
				_, body, ok := strings.Cut(b.Text, "\nOutput:\n")
				if ok {
					return firstString(skillStructuredStatus(decoded(strings.TrimSpace(body))), "pending")
				}
			}
		}
		return "pending"
	}
	if status != "pending" {
		return status
	}
	switch leaf {
	case "read", "read_file", "read_text", "get_file_contents", "view_file", "view", "skills__read", "skill":
		if result.ToolPhase == "tool.result" {
			for _, b := range result.Blocks {
				if b.Label == "Output" && strings.TrimSpace(b.Text) != "" && !strings.Contains(b.Text, "Content not exported") {
					return "read"
				}
			}
		}
	}
	return "pending"
}

func refreshSkillReads(ctx context.Context, tx *sql.Tx) error {
	for {
		rows, err := tx.QueryContext(ctx, `SELECT i.event_id,i.facts,i.start_key,i.result_key FROM pending_skill_reads d JOIN skill_read_inputs i ON i.event_id=d.event_id LIMIT 128`)
		if err != nil {
			return err
		}
		type item struct {
			id            string
			facts         []byte
			start, result string
		}
		batch := []item{}
		for rows.Next() {
			var i item
			if err = rows.Scan(&i.id, &i.facts, &i.start, &i.result); err != nil {
				break
			}
			batch = append(batch, i)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if rowErr != nil {
			return rowErr
		}
		if len(batch) == 0 {
			return nil
		}
		for _, i := range batch {
			e, err := scanEvent(tx.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM records WHERE id=?", i.id))
			if err != nil {
				return err
			}
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				return err
			}
			input := describe(e, doc)
			result := input
			matched := false
			if input.ToolPhase == "tool.start" && i.start != "" {
				event := TimelineEvent{Session: e.Session, Source: e.Source}
				starts, err := toolCandidates(ctx, tx, event, "", i.start)
				if err != nil {
					return err
				}
				outputs, err := toolCandidates(ctx, tx, event, "", i.result)
				if err != nil {
					return err
				}
				if len(starts) == 1 && len(outputs) == 1 {
					other, err := scanEvent(tx.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM records WHERE id=?", outputs[0]))
					if err != nil {
						return err
					}
					doc, err := decodeDocument(other.Raw)
					if err != nil {
						return err
					}
					result = describe(other, doc)
					_, _, valid := toolCallIdentity(other.Source, result)
					otherTool := skillString(firstValue(result.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe"), 512)
					matched = valid && (otherTool == "" || input.Title == otherTool)
				}
			} else if input.ToolPhase == "tool.result" {
				matched = true
				if i.result != "" {
					outputs, err := toolCandidates(ctx, tx, TimelineEvent{Session: e.Session, Source: e.Source}, "", i.result)
					if err != nil {
						return err
					}
					matched = len(outputs) == 1
				}
			}
			// Completed spans can carry both arguments and the result in one record.
			logical := e.ID
			if input.ToolPhase == "tool.result" && i.start != "" {
				starts, err := toolCandidates(ctx, tx, TimelineEvent{Session: e.Session, Source: e.Source}, "", i.start)
				if err != nil {
					return err
				}
				if len(starts) == 1 {
					logical = starts[0]
				}
			}
			err = tx.QueryRowContext(ctx, `SELECT id FROM logical_events WHERE id=? OR (otel_id=? AND import_id<>'') LIMIT 1`, logical, logical).Scan(&logical)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM skill_events WHERE event_id=? AND evidence IN ('read','pending','failed')`, e.ID); err != nil {
				return err
			}
			var facts []skillRead
			if err = json.Unmarshal(i.facts, &facts); err != nil {
				return err
			}
			for _, r := range facts {
				r.Evidence = "pending"
				if matched {
					r.Evidence = skillReadStatus(input, result, r)
				}
				group := identityKey(e.Session, "skill-read:"+r.Name, r.Plugin, logical)
				if err = insertSkillFact(ctx, tx, e, r.SkillEvidence, group); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM work_items WHERE kind='skill_reads' AND (scope,key) IN(SELECT event_id,'' FROM pending_skill_reads WHERE event_id=?)`, e.ID); err != nil {
				return err
			}
		}
	}
}

func storeSkillReadInput(ctx context.Context, tx *sql.Tx, e Event, d Detail) error {
	reads := eventSkillReads(d)
	if len(reads) == 0 {
		return nil
	}
	owner, call, valid := toolCallIdentity(e.Source, d)
	start, result := "", ""
	if valid && call != "" {
		start = identityKey(owner, "tool.start", "tool.call", call)
		result = identityKey(owner, "tool.result", "tool.call", call)
	}
	if !valid {
		for i := range reads {
			reads[i].Certain = false
		}
	}
	facts, err := json.Marshal(reads)
	if err != nil {
		return err
	}
	if _, err = execRecord(ctx, tx, `INSERT OR REPLACE INTO skill_read_inputs VALUES(?,?,?,?,?,?)`, e.ID, e.Session, e.Source, start, result, facts); err != nil {
		return err
	}
	if _, err = execRecord(ctx, tx, `INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('skill_reads',?,'')`, e.ID); err != nil {
		return err
	}
	for _, r := range reads {
		if err = insertSkillFact(ctx, tx, e, r.SkillEvidence, identityKey(e.Session, "skill-read:"+r.Name, r.Plugin, e.ID)); err != nil {
			return fmt.Errorf("skill read: %w", err)
		}
	}
	return nil
}
