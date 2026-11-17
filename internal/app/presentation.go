package app

import (
	"context"
	"database/sql"
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
	if view != "" && view != "activity" && view != "diagnostics" && view != "conversation" {
		return eventFilter{}, fmt.Errorf("invalid event view")
	}
	switch kind {
	case "", "conversation", "tool", "error", "model", "metric", "skill", "agent", "event":
	default:
		return eventFilter{}, fmt.Errorf("invalid event type")
	}
	if view == "conversation" {
		if kind != "" && kind != "conversation" {
			return eventFilter{}, fmt.Errorf("conversation view does not accept event-type filters")
		}
		kind = "conversation"
	}
	return eventFilter{Activity: view == "activity", Kind: kind}, nil
}

func filteredTimelineCount(ctx context.Context, tx *sql.Tx, session, origin string, filter eventFilter) (int, error) {
	query, args := "SELECT COUNT(*) FROM logical_events WHERE session=?", []any{session}
	if origin != "" {
		query = "SELECT COUNT(*) FROM record_facts WHERE session=? AND origin=?"
		args = append(args, origin)
	}
	var count int
	err := tx.QueryRowContext(ctx, query+filter.predicate(""), args...).Scan(&count)
	return count, err
}

func refreshPresentation(ctx context.Context, tx *sql.Tx) error {
	for {
		_, err := tx.ExecContext(ctx, `UPDATE logical_events AS t SET
   activity=MAX(COALESCE(i.activity,0),COALESCE(o.activity,0)),
   kind=COALESCE(i.kind,o.kind,'event'),
   has_skill=MAX(COALESCE(i.has_skill,0),COALESCE(o.has_skill,0)),
   conversation=MAX(COALESCE(i.conversation,0),COALESCE(o.conversation,0)),
   is_error=MAX(COALESCE(i.is_error,0),COALESCE(o.is_error,0))
   FROM (SELECT id FROM pending_presentation LIMIT 200) d
   JOIN logical_events previous ON previous.id=d.id
   LEFT JOIN record_facts i ON i.id=previous.import_id
   LEFT JOIN record_facts o ON o.id=previous.otel_id
   WHERE t.id=d.id`)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM work_items WHERE kind='presentation' AND (scope,key) IN(SELECT id,'' FROM pending_presentation WHERE id IN(SELECT id FROM pending_presentation LIMIT 200))")
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
	}
}
