package app

import (
	"time"
)

// Minute counters skip complete intervals of collapsed logs. The two boundary
// minutes still use the chronological index, retaining exact (time,sequence,id)
// placement even for equal timestamps and merged import/OTEL records.
func processingCountSQL(session, origin string, after, before *timelinePosition, ready bool) (string, []any) {
	scope, args := timelineScope(session, origin)
	scope += " AND conversation=0"
	if after != nil {
		scope, args = positionBound(scope, args, ">", *after)
	}
	if before != nil {
		scope, args = positionBound(scope, args, "<", *before)
	}
	direct := "SELECT COUNT(*),COALESCE(SUM(activity),0),COALESCE(SUM(is_error),0) FROM " + timelineTable(origin) + " WHERE " + scope
	if !ready {
		return direct, args
	}
	minute := func(p *timelinePosition) (string, string, bool) {
		if p == nil {
			return "", "", true
		}
		stamp, err := time.Parse(time.RFC3339Nano, p.Time)
		if err != nil || len(p.Time) < 16 {
			return "", "", false
		}
		// Stored timestamps are UTC. Noncanonical test/legacy values use exact scans.
		if stamp.Location() != time.UTC {
			return "", "", false
		}
		return p.Time[:16], stamp.Truncate(time.Minute).Add(time.Minute).Format("2006-01-02T15:04:05.000000000Z"), true
	}
	lower, nextLower, ok := minute(after)
	upper, nextUpper, upperOK := minute(before)
	if !ok || !upperOK || after != nil && before != nil && lower == upper {
		return direct, args
	}
	query := "SELECT COALESCE(SUM(count),0),COALESCE(SUM(activity),0),COALESCE(SUM(errors),0) FROM (SELECT count,activity,errors FROM processing_buckets WHERE session=? AND origin=?"
	values := []any{session, origin}
	if after != nil {
		query += " AND minute>?"
		values = append(values, lower)
	}
	if before != nil {
		query += " AND minute<?"
		values = append(values, upper)
	}
	for _, edge := range []struct {
		p            *timelinePosition
		minute, next string
	}{{after, lower, nextLower}, {before, upper, nextUpper}} {
		if edge.p == nil {
			continue
		}
		edgeScope, edgeArgs := timelineScope(session, origin)
		edgeScope += " AND conversation=0"
		if edge.p == after {
			edgeScope, edgeArgs = positionBound(edgeScope, edgeArgs, ">", *after)
			edgeScope += " AND time<?"
			edgeArgs = append(edgeArgs, edge.next)
		} else {
			edgeScope += " AND time>=?"
			edgeArgs = append(edgeArgs, edge.minute+":00.000000000Z")
			edgeScope, edgeArgs = positionBound(edgeScope, edgeArgs, "<", *before)
		}
		query += " UNION ALL SELECT COUNT(*),COALESCE(SUM(activity),0),COALESCE(SUM(is_error),0) FROM " + timelineTable(origin) + " WHERE " + edgeScope
		values = append(values, edgeArgs...)
	}
	return query + ")", values
}
