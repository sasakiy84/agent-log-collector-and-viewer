package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
)

type ModelCall struct {
	ID       string            `json:"id"`
	Model    string            `json:"model,omitempty"`
	Duration string            `json:"duration_ms,omitempty"`
	Values   map[string]string `json:"values"`
}

func spanDuration(r map[string]any) string {
	start, end := nanoKey(r["startTimeUnixNano"]), nanoKey(r["endTimeUnixNano"])
	if start == "" || end <= start {
		return ""
	}
	a, _ := new(big.Int).SetString(start, 10)
	b, _ := new(big.Int).SetString(end, 10)
	return decimalText(new(big.Rat).SetFrac(b.Sub(b, a), big.NewInt(1000000)))
}

func storeModelFact(ctx context.Context, tx *sql.Tx, e Event, doc document, p Detail) error {
	role := ""
	info := ModelCall{ID: e.ID, Model: str(p.Model), Values: map[string]string{}}
	a := doc.attributes()
	if e.Signal == "traces" && e.Trace != "" && e.Span != "" && str(a["websocket.warmup"]) != "true" {
		if p.Kind == "model" && (p.Name == "run_sampling_request" || str(a["gen_ai.operation.name"]) == "chat" || p.Name == "gen_ai.client.operation" || eventSuffix(p.Name) == "api_request" || eventSuffix(p.Name) == "llm_request") {
			info.Duration = spanDuration(doc.Record)
			if info.Duration != "" {
				role = "request"
			}
		}
		if usage := usageMeasurement(e, doc, p); usage != nil {
			for _, key := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "cost_usd"} {
				if value, ok := usage.Values[key]; ok {
					info.Values[key] = value
				}
			}
		}
		if role == "" && len(info.Values) > 0 {
			role = "usage"
		}
	}
	if role == "" {
		_, err := tx.ExecContext(ctx, "DELETE FROM model_facts WHERE id=?", e.ID)
		return err
	}
	root := ""
	if role == "request" {
		root = e.ID
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO model_facts VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
 session=excluded.session,source=excluded.source,origin=excluded.origin,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,
 turn=excluded.turn,role=excluded.role,root=CASE WHEN excluded.role='request' THEN excluded.id ELSE model_facts.root END,time=excluded.time,info=excluded.info`,
		e.ID, e.Session, e.Source, e.Origin, e.Trace, e.Span, e.ParentSpan, p.Turn, role, root, e.Time, string(encoded))
	if err != nil {
		return err
	}
	return err
}

// Follow explicit trace ancestry, accepting only a unique nearest request.
// No timestamps, token-value similarity or neighboring messages establish links.
func modelRoot(ctx context.Context, tx *sql.Tx, id, session, origin, source, trace, span string) (string, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE ancestors(span,depth) AS (
 SELECT ?,0 UNION
 SELECT e.parent_span,a.depth+1 FROM ancestors a CROSS JOIN events e ON e.source=? AND e.trace=? AND e.span=a.span
 WHERE e.signal='traces' AND e.session=? AND e.origin=? AND e.parent_span<>'' AND a.depth<128
 ) SELECT DISTINCT f.id,a.depth FROM ancestors a JOIN model_facts f ON f.source=? AND f.trace=? AND f.span=a.span
 WHERE f.role='request' AND f.session=? AND f.origin=? AND f.id<>? ORDER BY a.depth,f.id LIMIT 2`, span, source, trace, session, origin, source, trace, session, origin, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	root, depth := "", -1
	for rows.Next() {
		var candidate string
		var distance int
		if err = rows.Scan(&candidate, &distance); err != nil {
			return "", err
		}
		if root == "" {
			root, depth = candidate, distance
		} else if distance == depth {
			return "", nil
		}
	}
	return root, rows.Err()
}

func rebuildModelCall(ctx context.Context, tx *sql.Tx, id string) error {
	var session, origin, turn, time, encoded string
	err := tx.QueryRowContext(ctx, "SELECT session,origin,turn,time,info FROM model_facts WHERE id=? AND role='request'", id).Scan(&session, &origin, &turn, &time, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, "DELETE FROM model_calls WHERE id=?", id)
		return err
	}
	if err != nil {
		return err
	}
	var call ModelCall
	if err = json.Unmarshal([]byte(encoded), &call); err != nil {
		return err
	}
	own := map[string]string{}
	for key, value := range call.Values {
		own[key] = value
	}
	{
		rows, err := tx.QueryContext(ctx, "SELECT info FROM model_facts WHERE root=? AND role='usage' ORDER BY time,id", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var payload string
			if err = rows.Scan(&payload); err != nil {
				break
			}
			var usage ModelCall
			if err = json.Unmarshal([]byte(payload), &usage); err != nil {
				break
			}
			// These are response totals, not additive stream deltas. Newer
			// explicitly linked reports replace earlier fields without summing.
			for key, value := range usage.Values {
				if _, exists := own[key]; !exists {
					call.Values[key] = value
				}
			}
			if call.Model == "" {
				call.Model = usage.Model
			}
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}
	}
	payload, err := json.Marshal(call)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO model_calls VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session=excluded.session,origin=excluded.origin,turn=excluded.turn,time=excluded.time,info=excluded.info", id, session, origin, turn, time, string(payload))
	return err
}

func refreshModelCalls(ctx context.Context, tx *sql.Tx) error {
	if err := refreshModelAncestry(ctx, tx); err != nil {
		return err
	}
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM model_dirty LIMIT 200")
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
		requests := map[string]bool{}
		for _, id := range ids {
			var role, session, origin, source, trace, span, old string
			err := tx.QueryRowContext(ctx, "SELECT role,session,origin,source,trace,span,root FROM model_facts WHERE id=?", id).Scan(&role, &session, &origin, &source, &trace, &span, &old)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if role == "request" || errors.Is(err, sql.ErrNoRows) {
				requests[id] = true
			} else {
				root, err := modelRoot(ctx, tx, id, session, origin, source, trace, span)
				if err != nil {
					return err
				}
				if root != old {
					if _, err = tx.ExecContext(ctx, "UPDATE model_facts SET root=? WHERE id=?", root, id); err != nil {
						return err
					}
				}
				if root != "" {
					requests[root] = true
				}
				if old != "" {
					requests[old] = true
				}
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM model_dirty WHERE id=?", id); err != nil {
				return err
			}
		}
		for id := range requests {
			if err = rebuildModelCall(ctx, tx, id); err != nil {
				return err
			}
		}
	}
}

func attachModelCalls(ctx context.Context, tx *sql.Tx, events []TimelineEvent, origin string) error {
	for i := range events {
		e := &events[i]
		if e.Detail.Kind == "model" {
			for _, record := range e.Records {
				var encoded string
				err := tx.QueryRowContext(ctx, "SELECT info FROM model_calls WHERE id=?", record.ID).Scan(&encoded)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				var call ModelCall
				if err = json.Unmarshal([]byte(encoded), &call); err != nil {
					return err
				}
				e.ModelCalls = append(e.ModelCalls, call)
			}
			continue
		}
		invocation := e.Source == "copilot" && e.Detail.Kind == "agent" && e.Trace != "" && e.Span != ""
		if (e.Detail.Kind != "assistant" && !invocation) || e.Detail.Turn == "" {
			continue
		}
		if !invocation {
			var latest string
			err := tx.QueryRowContext(ctx, "SELECT id FROM event_cards WHERE session=? AND turn=? AND kind='assistant' AND (?='' OR origin=?) ORDER BY time DESC,sequence DESC,id DESC LIMIT 1", e.Session, e.Detail.Turn, origin, origin).Scan(&latest)
			if err != nil {
				return err
			}
			owns := false
			for _, record := range e.Records {
				owns = owns || record.ID == latest
			}
			if !owns {
				continue
			}
		}
		query := "SELECT m.info FROM model_calls m WHERE m.session=? AND m.turn=? AND (?='' OR m.origin=?)"
		args := []any{e.Session, e.Detail.Turn, origin, origin}
		if invocation {
			query += " AND EXISTS(SELECT 1 FROM events request WHERE request.id=m.id AND request.trace=? AND request.parent_span=?)"
			args = append(args, e.Trace, e.Span)
		}
		rows, err := tx.QueryContext(ctx, query+" ORDER BY m.time,m.id LIMIT 101", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var encoded string
			var call ModelCall
			if err = rows.Scan(&encoded); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(encoded), &call); err != nil {
				break
			}
			if len(e.ModelCalls) == 100 {
				e.ModelCallsMore = true
				break
			}
			e.ModelCalls = append(e.ModelCalls, call)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}
	}
	return nil
}
