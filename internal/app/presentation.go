package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type eventFilter struct {
	Activity bool
	Kind     string
}

func (f eventFilter) predicate(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	parts := []string{}
	if f.Activity {
		parts = append(parts, prefix+"activity=1")
	}
	switch f.Kind {
	case "conversation":
		parts = append(parts, prefix+"conversation=1")
	case "skill":
		parts = append(parts, prefix+"has_skill=1")
	case "error":
		parts = append(parts, prefix+"is_error=1")
	case "":
	default:
		parts = append(parts, prefix+"kind='"+f.Kind+"'")
	}
	if len(parts) == 0 {
		return ""
	}
	return " AND " + strings.Join(parts, " AND ")
}

func parseEventFilter(view, kind string) (eventFilter, error) {
	if view != "" && view != "activity" && view != "diagnostics" {
		return eventFilter{}, fmt.Errorf("invalid event view")
	}
	switch kind {
	case "", "conversation", "tool", "error", "model", "metric", "skill", "agent", "event":
	default:
		return eventFilter{}, fmt.Errorf("invalid event type")
	}
	return eventFilter{Activity: view == "activity", Kind: kind}, nil
}

func filteredTimelineCount(ctx context.Context, tx *sql.Tx, session, origin string, filter eventFilter) (int, error) {
	query, args := "SELECT COUNT(*) FROM timeline_entries WHERE session=?", []any{session}
	if origin != "" {
		query = "SELECT COUNT(*) FROM event_cards WHERE session=? AND origin=?"
		args = append(args, origin)
	}
	var count int
	err := tx.QueryRowContext(ctx, query+filter.predicate(""), args...).Scan(&count)
	return count, err
}

func refreshPresentation(ctx context.Context, tx *sql.Tx) error {
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM presentation_dirty LIMIT 200")
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil || len(ids) == 0 {
			return err
		}
		for _, id := range ids {
			_, err = tx.ExecContext(ctx, `UPDATE timeline_entries SET
 activity=MAX(COALESCE((SELECT activity FROM event_cards WHERE id=import_id),0),COALESCE((SELECT activity FROM event_cards WHERE id=otel_id),0)),
 kind=COALESCE((SELECT kind FROM event_cards WHERE id=import_id),(SELECT kind FROM event_cards WHERE id=otel_id),'event'),
 has_skill=MAX(COALESCE((SELECT has_skill FROM event_cards WHERE id=import_id),0),COALESCE((SELECT has_skill FROM event_cards WHERE id=otel_id),0)),
 conversation=MAX(COALESCE((SELECT conversation FROM event_cards WHERE id=import_id),0),COALESCE((SELECT conversation FROM event_cards WHERE id=otel_id),0)),
 is_error=MAX(COALESCE((SELECT is_error FROM event_cards WHERE id=import_id),0),COALESCE((SELECT is_error FROM event_cards WHERE id=otel_id),0)) WHERE id=?`, id)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM presentation_dirty WHERE id=?", id); err != nil {
				return err
			}
		}
	}
}

func backfillPresentation(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.session,e.parent,e.source,e.signal,e.time,e.name,e.trace,e.span,e.parent_span,
 CAST(CASE WHEN c.origin='otel' THEN e.raw ELSE 'null' END AS BLOB),e.sequence,c.origin,c.detail
 FROM event_cards c CROSS JOIN events e ON e.id=c.id WHERE c.id>? ORDER BY c.id LIMIT 200`, cursor)
	if err != nil {
		return cursor, 0, err
	}
	type cached struct {
		e Event
		d Detail
	}
	batch, size := []cached{}, 0
	for rows.Next() {
		var item cached
		var encoded []byte
		args := append(item.e.scanArgs(), &encoded)
		if err = rows.Scan(args...); err != nil {
			break
		}
		if err = json.Unmarshal(encoded, &item.d); err != nil {
			break
		}
		batch = append(batch, item)
		size += len(item.e.Raw) + len(encoded)
		if size >= 8<<20 {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return cursor, 0, err
	}
	for _, item := range batch {
		e, d := item.e, item.d
		if e.Origin == "otel" {
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				return cursor, 0, err
			}
			d = describe(e, doc)
			tool := str(firstValue(d.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe"))
			result, err := tx.ExecContext(ctx, "UPDATE events SET kind=?,tool_name=? WHERE id=? AND (kind<>? OR tool_name<>?) AND NOT EXISTS(SELECT 1 FROM archive_locks WHERE session=events.session)", d.Kind, tool, e.ID, d.Kind, tool)
			if err != nil {
				return cursor, 0, err
			}
			if n, _ := result.RowsAffected(); n != 0 {
				if _, err = tx.ExecContext(ctx, "DELETE FROM event_keys WHERE event_id=?", e.ID); err != nil {
					return cursor, 0, err
				}
				for _, key := range correlationKeys(e, d, doc) {
					if _, err = tx.ExecContext(ctx, "INSERT INTO event_keys(event_id,namespace,key) VALUES(?,?,?)", e.ID, key.Namespace, key.Key); err != nil {
						return cursor, 0, err
					}
				}
			}
			if err = storeCard(ctx, tx, e, d, doc); err != nil {
				return cursor, 0, err
			}
		} else {
			d.Diagnostic = diagnosticDetail(e, d)
			encoded, err := json.Marshal(d)
			if err != nil {
				return cursor, 0, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET detail=?,activity=?,kind=?,is_error=?,turn=?,conversation=?,has_skill=EXISTS(SELECT 1 FROM skill_events WHERE event_id=?) WHERE id=?", encoded, detailActivity(d), d.Kind, d.Error, d.Turn, detailConversation(d), e.ID, e.ID); err != nil {
				return cursor, 0, err
			}
		}
		cursor = e.ID
	}
	if err = refreshProjections(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, len(batch), nil
}
