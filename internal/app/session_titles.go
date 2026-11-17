package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Codex can store automatically injected context as user-role messages. Strip
// only recognized, complete leading envelopes; ordinary markup stays intact.
func userPromptText(text, source string) string {
	text, _ = userPromptContent(text, source)
	return text
}

// Also report removed page context so context-only sessions can be classified
// without treating their user-role envelopes as human requests.
func userPromptContent(text, source string) (string, bool) {
	pageContext := false
	text = strings.TrimSpace(text)
	if source == "copilot" && strings.HasPrefix(text, "<current_datetime>") {
		if end := strings.Index(text, "</current_datetime>"); end >= 0 {
			if _, err := time.Parse(time.RFC3339Nano, text[len("<current_datetime>"):end]); err == nil {
				text = strings.TrimSpace(text[end+len("</current_datetime>"):])
			}
		}
	}
	if source != "codex" {
		return text, false
	}
	for {
		previous := text
		for _, tag := range []string{"recommended_plugins", "environment_context", "user_instructions", "skills_instructions", "skill"} {
			open, close := "<"+tag+">", "</"+tag+">"
			if strings.HasPrefix(text, open) {
				if end := strings.Index(text[len(open):], close); end >= 0 {
					text = strings.TrimSpace(text[len(open)+end+len(close):])
				}
			}
		}
		if rest, ok := codexPageContext(text); ok {
			text = rest
			pageContext = true
		}
		if strings.HasPrefix(text, "# AGENTS.md instructions for ") {
			if start := strings.Index(text, "\n"); start >= 0 {
				body := strings.TrimSpace(text[start:])
				if strings.HasPrefix(body, "<INSTRUCTIONS>") {
					if end := strings.Index(body, "</INSTRUCTIONS>"); end >= 0 {
						text = strings.TrimSpace(body[end+len("</INSTRUCTIONS>"):])
					}
				}
			}
		}
		if text == previous {
			return text, pageContext
		}
	}
}

// App page snapshots have numbered envelopes. Match a complete leading pair,
// not arbitrary external_* markup or a user's discussion of the protocol.
func codexPageContext(text string) (string, bool) {
	if !strings.HasPrefix(text, "<external_codex_apps_") {
		return text, false
	}
	open, _, ok := strings.Cut(text, ">")
	if !ok {
		return text, false
	}
	tag := strings.TrimPrefix(open, "<")
	if tag != "external_codex_apps_open_page" {
		parts, ok := strings.CutPrefix(tag, "external_codex_apps_selected_page_part_")
		part, total, split := strings.Cut(parts, "_of_")
		n, nErr := strconv.ParseUint(part, 10, 64)
		m, mErr := strconv.ParseUint(total, 10, 64)
		if !ok || !split || nErr != nil || mErr != nil || n == 0 || n > m {
			return text, false
		}
	}
	close := "</" + tag + ">"
	end := strings.Index(text[len(open)+1:], close)
	if end < 0 {
		return text, false
	}
	return strings.TrimSpace(text[len(open)+1+end+len(close):]), true
}

func promptTitle(source string, d Detail) string {
	if d.Kind != "user" && !(source == "copilot" && d.Kind == "agent" && d.SessionPrompt) {
		return ""
	}
	for _, block := range userRequestBlocks(d) {
		if block.Label != "Request" && block.Label != "user" {
			continue
		}
		text := userPromptText(block.Text, source)
		if text == "" || text == "Content not exported" || text == "<REDACTED>" || text == "[REDACTED]" || source == "codex" && codexAutomaticPrompt(text, d.Model) {
			continue
		}
		line, _, _ := strings.Cut(text, "\n")
		return clip(strings.TrimSpace(line), 256)
	}
	return ""
}

func userRequestBlocks(d Detail) []Block {
	blocks := d.Blocks
	direct := firstValue(d.Attributes, "prompt_text", "prompt", "user_prompt")
	if present(direct) {
		blocks = []Block{{Label: "Request", Text: display(direct)}}
	}
	if messages, ok := decoded(d.Attributes["gen_ai.input.messages"]).([]any); ok {
		request := Detail{Kind: "user"}
		for _, message := range messages {
			role := str(obj(message)["role"])
			if role == "user" || role == "" {
				request.messages("Input", []any{message})
			}
		}
		if present(direct) {
			blocks = append(blocks, request.Blocks...)
		} else {
			blocks = request.Blocks
		}
	}
	return blocks
}

// Titles read one indexed, bounded card. They never decode original histories
// on a list request, and context-only sessions fall back to their session ID.
func sessionPreview(ctx context.Context, tx *sql.Tx, session, origin string) (Detail, error) {
	query := "SELECT detail,prompt_title FROM record_facts WHERE session=? AND prompt_title<>''"
	args := []any{session}
	if origin != "" {
		query += " AND origin=?"
		args = append(args, origin)
	}
	var raw []byte
	var title string
	err := tx.QueryRowContext(ctx, query+" ORDER BY time,sequence,id LIMIT 1", args...).Scan(&raw, &title)
	if err == sql.ErrNoRows {
		rows, err := readTimeline(ctx, tx, session, origin, 1, 0, timelinePreview)
		if err != nil {
			return Detail{}, err
		}
		if len(rows) == 0 {
			return Detail{}, sql.ErrNoRows
		}
		d := summaryDetail(rows[0].Detail)
		d.Blocks = []Block{}
		// Some providers report the conversation only inside a model span.
		// Retain its explicitly user-labelled input as a bounded fallback.
		if rows[0].Detail.Kind != "user" {
			input := rows[0].Detail
			input.Kind = "user"
			if title := promptTitle(rows[0].Source, input); title != "" {
				d.Blocks = []Block{{Label: "Request", Text: title}}
			}
		}
		return d, nil
	}
	if err != nil {
		return Detail{}, err
	}
	var d Detail
	if err := json.Unmarshal(raw, &d); err != nil {
		return Detail{}, err
	}
	d = summaryDetail(d)
	d.Blocks = []Block{{Label: "Request", Text: title}}
	return d, nil
}

func catalogPreview(ctx context.Context, tx *sql.Tx, session string) ([]byte, error) {
	detail, err := sessionPreview(ctx, tx, session, "")
	if err != nil {
		return nil, err
	}
	detail.Cwd, err = sessionDirectory(ctx, tx, session)
	if err != nil {
		return nil, err
	}
	return json.Marshal(detail)
}
