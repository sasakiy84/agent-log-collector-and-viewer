package app

import (
	"context"
	"database/sql"
	"strings"
)

// Filtering and matching precede every count and page. A candidate is accepted
// only if both records have exactly one possible counterpart. This prevents
// repeated IDs or multiple telemetry projections from silently hiding records.
const timelineCTE = `WITH eligible AS MATERIALIZED (
 SELECT id,session,parent,source,time,sequence,origin,kind,tool_name FROM events WHERE (?='' OR origin=?) AND (?='' OR session=?)
), candidates AS MATERIALIZED (
 SELECT DISTINCT i.id AS imported,o.id AS otel
 FROM eligible i JOIN event_keys ik ON ik.event_id=i.id
 JOIN event_keys ok ON ok.key=ik.key
 JOIN eligible o ON o.id=ok.event_id AND o.origin='otel' AND o.session=i.session AND o.source=i.source
 WHERE i.origin='import' AND (i.tool_name='' OR o.tool_name='' OR i.tool_name=o.tool_name)
 AND NOT EXISTS (SELECT 1 FROM event_keys x JOIN event_keys y ON y.event_id=o.id AND y.namespace=x.namespace WHERE x.event_id=i.id AND x.key<>y.key)
), pairs AS MATERIALIZED (
 SELECT c.* FROM candidates c WHERE
 c.imported IN (SELECT imported FROM candidates GROUP BY imported HAVING COUNT(*)=1) AND
 c.otel IN (SELECT otel FROM candidates GROUP BY otel HAVING COUNT(*)=1)
), members AS MATERIALIZED (
 SELECT e.*,COALESCE(pi.imported,po.imported,e.id) AS view_id FROM eligible e
 LEFT JOIN pairs pi ON e.id=pi.imported LEFT JOIN pairs po ON e.id=po.otel
), timeline AS MATERIALIZED (
 SELECT view_id AS id,session,MAX(source) AS source,MAX(parent) AS parent,
 COALESCE(MIN(NULLIF(time,'1970-01-01T00:00:00.000000000Z')),MIN(time)) AS time,
 MAX(time) AS end,MAX(sequence) AS sequence,MAX(kind='user') AS is_user
 FROM members GROUP BY view_id,session
) `

func projectionArgs(origin, session string) []any { return []any{origin, origin, session, session} }

const eventColumns = "id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence,origin"

type scanner interface{ Scan(...any) error }

func (e *Event) scanArgs() []any {
	return []any{&e.ID, &e.Session, &e.Parent, &e.Source, &e.Signal, &e.Time, &e.Name, &e.Trace, &e.Span, &e.ParentSpan, &e.Raw, &e.Sequence, &e.Origin}
}
func scanEvent(row scanner) (Event, error) {
	var e Event
	err := row.Scan(e.scanArgs()...)
	return e, err
}
func readProjection(ctx context.Context, tx *sql.Tx, session, origin string, limit, off int, preview bool) ([]TimelineEvent, error) {
	order := "time,sequence,id"
	if preview {
		order = "is_user DESC,time,sequence,id"
	}
	args := append(projectionArgs(origin, session), limit, off)
	rows, err := tx.QueryContext(ctx, timelineCTE+`SELECT m.view_id,`+qualifiedColumns("r")+` FROM members m JOIN
 (SELECT id,time,sequence FROM timeline ORDER BY `+order+` LIMIT ? OFFSET ?) page ON page.id=m.view_id
 JOIN events r ON r.id=m.id
 ORDER BY page.time,page.sequence,page.id,m.origin,m.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []struct {
		id      string
		records []Event
	}{}
	for rows.Next() {
		var id string
		var e Event
		if err := rows.Scan(append([]any{&id}, e.scanArgs()...)...); err != nil {
			return nil, err
		}
		if len(groups) == 0 || groups[len(groups)-1].id != id {
			groups = append(groups, struct {
				id      string
				records []Event
			}{id, nil})
		}
		groups[len(groups)-1].records = append(groups[len(groups)-1].records, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TimelineEvent, 0, len(groups))
	for _, g := range groups {
		event, err := projectEvent(g.id, g.records)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, nil
}
func qualifiedColumns(alias string) string {
	cols := strings.Split(eventColumns, ",")
	for i := range cols {
		cols[i] = alias + "." + cols[i]
	}
	return strings.Join(cols, ",")
}
