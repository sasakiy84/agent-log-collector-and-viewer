package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Codex can store automatically injected context as user-role messages. Strip
// only recognized, complete leading envelopes; ordinary markup stays intact.
func userPromptText(text, source string) string {
	text = strings.TrimSpace(text)
	if source != "codex" {
		return text
	}
	for {
		previous := text
		for _, tag := range []string{"recommended_plugins", "environment_context", "user_instructions", "skills_instructions", "skill", "external_codex_apps_open_page"} {
			open, close := "<"+tag+">", "</"+tag+">"
			if strings.HasPrefix(text, open) {
				if end := strings.Index(text[len(open):], close); end >= 0 {
					text = strings.TrimSpace(text[len(open)+end+len(close):])
				}
			}
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
			return text
		}
	}
}

func promptTitle(source string, d Detail) string {
	if d.Kind != "user" {
		return ""
	}
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
	for _, block := range blocks {
		if block.Label != "Request" && block.Label != "user" {
			continue
		}
		text := userPromptText(block.Text, source)
		if text == "" || text == "Content not exported" {
			continue
		}
		line, _, _ := strings.Cut(text, "\n")
		return clip(strings.TrimSpace(line), 256)
	}
	return ""
}

// Titles read one indexed, bounded card. They never decode original histories
// on a list request, and context-only sessions fall back to their session ID.
func sessionPreview(ctx context.Context, tx *sql.Tx, session, origin string) (Detail, error) {
	query := "SELECT detail,prompt_title FROM event_cards WHERE session=? AND prompt_title<>''"
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

func backfillTitles(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+qualifiedColumns("e")+" FROM event_cards c CROSS JOIN events e ON e.id=c.id WHERE c.id>? AND c.is_user=1 ORDER BY c.id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
	}
	return updatePromptTitles(ctx, tx, batch, cursor)
}

const appMessageTitlesJob = "codex_app_titles.v1"

func backfillAppMessageTitles(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	// Page through the existing user-card index in bounded transactions. Read
	// original payloads only for titles affected by the newly recognized tag.
	columns := strings.Replace(qualifiedColumns("e"), "e.raw", `CASE WHEN c.source='codex' AND c.prompt_title GLOB '<external_codex_apps_open_page>*' THEN e.raw ELSE X'' END`, 1)
	batch, err := loadEventBatch(ctx, tx, "SELECT "+columns+" FROM event_cards c CROSS JOIN events e ON e.id=c.id WHERE c.id>? AND c.is_user=1 ORDER BY c.id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
	}
	return updatePromptTitles(ctx, tx, batch, cursor)
}

func updatePromptTitles(ctx context.Context, tx *sql.Tx, batch []Event, cursor string) (string, int, error) {
	sessions := map[string]bool{}
	for _, event := range batch {
		cursor = event.ID
		if len(event.Raw) == 0 {
			continue
		}
		doc, err := decodeDocument(event.Raw)
		if err != nil {
			return cursor, 0, err
		}
		title := promptTitle(event.Source, describe(event, doc))
		if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET prompt_title=? WHERE id=? AND prompt_title<>?", title, event.ID, title); err != nil {
			return cursor, 0, err
		}
		sessions[event.Session] = true
	}
	for session := range sessions {
		preview, err := catalogPreview(ctx, tx, session)
		if err != nil {
			return cursor, 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE session_catalog SET preview=? WHERE session=?", preview, session); err != nil {
			return cursor, 0, err
		}
	}
	// Title changes can alter cached preview metadata, so publish summaries in
	// the same transaction without scanning unrelated session histories.
	_, err := refreshFolderBatch(ctx, tx)
	return cursor, len(batch), err
}
