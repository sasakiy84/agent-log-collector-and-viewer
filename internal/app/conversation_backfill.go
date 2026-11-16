package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const conversationJob = "conversation.v1"

// Visit bounded cached presentations, not entire original payloads. Only
// Guardian observations require reparsing; completed jobs are never repeated.
func backfillConversation(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,source,origin,detail,conversation,prompt_title FROM event_cards WHERE id>? ORDER BY id LIMIT 128", cursor)
	if err != nil {
		return cursor, 0, err
	}
	type cached struct {
		id, source, origin, title string
		d                         Detail
		conversation              bool
	}
	batch := []cached{}
	for rows.Next() {
		var c cached
		var encoded []byte
		if err = rows.Scan(&c.id, &c.source, &c.origin, &encoded, &c.conversation, &c.title); err != nil {
			break
		}
		if err = json.Unmarshal(encoded, &c.d); err != nil {
			break
		}
		batch = append(batch, c)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return cursor, 0, err
	}
	processed, size := 0, 0
	for _, c := range batch {
		guardian := c.source == "codex" && guardianInternal(c.d.Name, c.d.Attributes)
		// Old bounded cards omit namespace attributes. Check unknown native
		// tool-span names in the original before deciding they really are tools.
		unknownTool := c.source == "codex" && c.origin == "otel" && c.d.Kind == "tool" && !isToolEvent(c.d.Name, nil)
		// A cached block can end inside a long injected context envelope.
		// Empty-title user records need the original to classify that safely.
		contextOnly := c.source == "codex" && c.d.Kind == "user" && c.title == ""
		copilotRoot := c.source == "copilot" && c.d.Kind == "agent" && !c.d.SessionPrompt
		if guardian || contextOnly || unknownTool || copilotRoot {
			e, err := scanEvent(tx.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM events WHERE id=?", c.id))
			if err != nil {
				return cursor, 0, err
			}
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				return cursor, 0, err
			}
			size += len(e.Raw)
			guardian = guardian || e.Source == "codex" && guardianInternal(str(doc.Record["name"]), doc.attributes())
			// Dirty both sides before removing false identities. A previously
			// merged real tool result must reappear as its own timeline card.
			if guardian {
				_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO read_model_dirty
 SELECT session,import_id FROM timeline_entries WHERE (import_id=? OR otel_id=?) AND import_id<>''
 UNION ALL SELECT session,otel_id FROM timeline_entries WHERE (import_id=? OR otel_id=?) AND otel_id<>''`, e.ID, e.ID, e.ID, e.ID)
				if err == nil {
					_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO read_model_dirty VALUES(?,?)", e.Session, e.ID)
				}
				if err == nil {
					_, err = tx.ExecContext(ctx, "DELETE FROM event_keys WHERE event_id=?", e.ID)
				}
				if err != nil {
					return cursor, 0, err
				}
			}
			if err = storeCard(ctx, tx, e, describe(e, doc), doc); err != nil {
				return cursor, 0, err
			}
		} else if conversation := detailConversation(c.d, c.source); conversation != c.conversation {
			if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET conversation=? WHERE id=?", conversation, c.id); err != nil {
				return cursor, 0, err
			}
		}
		cursor = c.id
		processed++
		if size >= 8<<20 {
			break
		}
	}
	if err = refreshProjections(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, processed, nil
}
