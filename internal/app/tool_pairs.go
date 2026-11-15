package app

import (
	"context"
	"database/sql"
)

type ToolCounterpart struct {
	ID     string `json:"id"`
	Time   string `json:"time"`
	Detail Detail `json:"detail"`
}
type ToolPair struct {
	State       string           `json:"state"`
	Phase       string           `json:"phase,omitempty"`
	Counterpart *ToolCounterpart `json:"counterpart,omitempty"`
}

// Existing phase-scoped tool.call keys also support input/output navigation.
// Fetch at most two logical candidates: a reused identifier is ambiguous, not
// permission to pick whichever event happens to be nearest in time.
func toolCandidates(ctx context.Context, tx *sql.Tx, event TimelineEvent, origin, key string) ([]string, error) {
	id := "e.id"
	if origin == "" {
		id = `COALESCE((SELECT id FROM timeline_entries t WHERE t.otel_id=e.id AND t.import_id<>'' AND t.otel_id<>''),e.id)`
	}
	query := `SELECT DISTINCT ` + id + ` FROM event_keys k CROSS JOIN events e ON e.id=k.event_id WHERE k.namespace='tool.call' AND k.key=? AND e.session=? AND e.source=?`
	args := []any{key, event.Session, event.Source}
	if origin != "" {
		query += " AND e.origin=?"
		args = append(args, origin)
	}
	rows, err := tx.QueryContext(ctx, query+" LIMIT 2", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func attachToolPair(ctx context.Context, tx *sql.Tx, event *TimelineEvent, origin string) error {
	if event.Detail.Kind != "tool" {
		return nil
	}
	pair := &ToolPair{State: "unavailable", Phase: event.Detail.ToolPhase}
	event.ToolPair = pair
	if pair.Phase != "tool.start" && pair.Phase != "tool.result" {
		return nil
	}
	owner, valid := agentIdentity(event.Source, event.Detail.Attributes)
	if !valid {
		pair.State = "ambiguous"
		return nil
	}
	call := ""
	for _, key := range []string{"gen_ai.tool.call.id", "tool_call_id", "call_id", "tool_use_id"} {
		value := event.Detail.Attributes[key]
		if !present(value) {
			continue
		}
		id, ok := value.(string)
		if !ok || call != "" && id != call {
			pair.State = "ambiguous"
			return nil
		}
		call = id
	}
	if call == "" {
		pair.State = "no_id"
		input, output := false, false
		for _, b := range event.Detail.Blocks {
			input = input || b.Label == "Input" || b.Label == "Command" || b.Label == "File"
			output = output || b.Label == "Output"
		}
		if input && output {
			pair.State = "embedded"
		}
		return nil
	}
	otherPhase := "tool.start"
	if pair.Phase == "tool.start" {
		otherPhase = "tool.result"
	}
	ids, err := toolCandidates(ctx, tx, *event, origin, identityKey(owner, otherPhase, "tool.call", call))
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		pair.State = "missing"
		input, output := false, false
		for _, b := range event.Detail.Blocks {
			input = input || b.Label == "Input" || b.Label == "Command" || b.Label == "File"
			output = output || b.Label == "Output"
		}
		if input && output {
			pair.State = "embedded"
		}
		return nil
	}
	if len(ids) != 1 {
		pair.State = "ambiguous"
		return nil
	}
	own, err := toolCandidates(ctx, tx, *event, origin, identityKey(owner, pair.Phase, "tool.call", call))
	if err != nil {
		return err
	}
	if len(own) != 1 || own[0] != event.ID {
		pair.State = "ambiguous"
		return nil
	}
	query := `SELECT t.id,` + qualifiedCardColumns("c") + ` FROM timeline_entries t JOIN event_cards c ON c.id=t.import_id OR c.id=t.otel_id WHERE t.session=? AND t.id=? ORDER BY c.origin,c.id`
	args := []any{event.Session, ids[0]}
	if origin != "" {
		query = "SELECT id," + cardColumns + " FROM event_cards WHERE session=? AND id=? AND origin=?"
		args = append(args, origin)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	related, err := decodeTimeline(rows)
	if err != nil {
		return err
	}
	if len(related) != 1 {
		pair.State = "missing"
		return nil
	}
	other := related[0]
	tool := str(firstValue(event.Detail.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe"))
	if tool != "" && other.Detail.Title != other.Detail.Name && tool != other.Detail.Title {
		pair.State = "ambiguous"
		return nil
	}
	pair.State = "paired"
	pair.Counterpart = &ToolCounterpart{ID: other.ID, Time: other.Time, Detail: other.Detail}
	return nil
}
