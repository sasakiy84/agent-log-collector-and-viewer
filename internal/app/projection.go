package app

import (
	"strings"
)

// A pair is accepted only when both originals have exactly one compatible
// counterpart, so repeated or contradictory identifiers remain visible.
// Keep changed IDs as the outer loop. A session with millions of unrelated
// events still requires only primary-key reads for the affected component.
const componentCTE = `WITH eligible AS MATERIALIZED (
 SELECT e.id,e.session,e.parent,e.source,e.time,e.sequence,e.origin,e.kind,e.tool_name
 FROM read_model_dirty d CROSS JOIN events e ON e.id=d.id
 WHERE d.session=? AND e.session=?
) ` + timelineProjection

const timelineProjection = `, candidates AS MATERIALIZED (
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
func qualifiedColumns(alias string) string {
	cols := strings.Split(eventColumns, ",")
	for i := range cols {
		cols[i] = alias + "." + cols[i]
	}
	return strings.Join(cols, ",")
}
