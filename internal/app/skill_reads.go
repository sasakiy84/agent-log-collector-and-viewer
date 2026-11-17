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
	// A single outer tool call contributes at most one attempt per skill.
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

// Chunk IDs are executor metadata, never inferred from skill text. Nested
// results may be arrays or printed JSON values from functions.exec.
var skillChunkHeader = regexp.MustCompile(`(?m)^Chunk ID: ([A-Za-z0-9_-]+)\r?$`)

func skillExecutionChunks(d Detail) []string {
	leaf := strings.ToLower(d.Title)
	leaf = leaf[strings.LastIndex(leaf, ".")+1:]
	switch leaf {
	case "exec", "exec_command", "shell", "bash", "run_shell_command", "run_in_terminal", "terminal":
	default:
		return nil
	}
	chunks := map[string]bool{}
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if chunk, ok := v["chunk_id"].(string); ok && len(chunk) > 0 && len(chunk) <= 128 {
				chunks[chunk] = true
			}
			if text, ok := v["text"].(string); ok {
				visit(text)
			}
			for _, key := range []string{"result", "value"} {
				if nested, ok := v[key]; ok {
					visit(nested)
				}
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		case string:
			if strings.HasPrefix(v, "Chunk ID:") {
				if m := skillChunkHeader.FindStringSubmatch(v); len(m) == 2 {
					chunks[m[1]] = true
				}
				return
			}
			if strings.HasPrefix(v, "Script completed\n") {
				_, body, ok := strings.Cut(v, "\nOutput:\n")
				if !ok {
					return
				}
				decoder := json.NewDecoder(strings.NewReader(body))
				for {
					var item any
					if decoder.Decode(&item) != nil {
						break
					}
					visit(item)
				}
				return
			}
			if strings.HasPrefix(strings.TrimSpace(v), "[") {
				var items []any
				if json.Unmarshal([]byte(v), &items) == nil {
					visit(items)
				}
			}
		}
	}
	for _, block := range d.Blocks {
		if block.Label == "Output" {
			visit(block.Text)
		}
	}
	out := make([]string, 0, len(chunks))
	for chunk := range chunks {
		out = append(out, chunk)
	}
	return out
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
					otherDetail := describe(other, doc)
					otherTool := skillString(firstValue(otherDetail.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe"), 512)
					if _, _, valid := toolCallIdentity(other.Source, otherDetail); valid && (otherTool == "" || input.Title == otherTool) {
						result = otherDetail
					}
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
			if _, err = tx.ExecContext(ctx, `DELETE FROM skill_events WHERE event_id=? AND evidence IN ('read','pending','failed','attempt')`, e.ID); err != nil {
				return err
			}
			var facts []skillRead
			if err = json.Unmarshal(i.facts, &facts); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM skill_attempt_links WHERE event_id=?", e.ID); err != nil {
				return err
			}
			owner, _, valid := toolCallIdentity(e.Source, input)
			if valid {
				wrapper := input.Title == "exec" || input.Title == "functions.exec"
				execution := result
				execution.Title = input.Title
				for _, chunk := range skillExecutionChunks(execution) {
					if _, err = tx.ExecContext(ctx, "INSERT INTO skill_attempt_links VALUES(?,?,?,?,?,?)", e.ID, e.Session, e.Source, owner, chunk, wrapper); err != nil {
						return err
					}
				}
			}
			for _, r := range facts {
				r.Evidence = "attempt"
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
