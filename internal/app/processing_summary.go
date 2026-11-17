package app

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

const processingCardsJob = "processing.cards.v1"
const processingTimelineJob = "processing.timeline.v1"

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

func processingSummaryReady(ctx context.Context, tx *sql.Tx, origin string) (bool, error) {
	job := processingTimelineJob
	if origin != "" {
		job = processingCardsJob
	}
	var ready bool
	err := tx.QueryRowContext(ctx, "SELECT complete FROM maintenance_jobs WHERE name=?", job).Scan(&ready)
	return ready, err
}

func backfillProcessing(table string) maintenanceBatch {
	return func(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
		origin := "origin"
		if table == "timeline_entries" {
			origin = "''"
		}
		// Resolve a bounded primary-key page before grouping. Only chronological keys
		// and flags are loaded, never card bodies or raw JSON.
		rows, err := tx.QueryContext(ctx, "SELECT id FROM "+table+" WHERE id>? ORDER BY id LIMIT 1000", cursor)
		if err != nil {
			return cursor, 0, err
		}
		next, count := cursor, 0
		for rows.Next() {
			if err = rows.Scan(&next); err != nil {
				break
			}
			count++
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil || count == 0 {
			return cursor, count, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO processing_buckets
 SELECT session,`+origin+`,substr(time,1,16),COUNT(*),SUM(activity),SUM(is_error)
 FROM `+table+` WHERE id>? AND id<=? AND conversation=0 GROUP BY session,`+origin+`,substr(time,1,16)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+excluded.count,activity=activity+excluded.activity,errors=errors+excluded.errors`, cursor, next)
		return next, count, err
	}
}

func (a *App) startProcessingIndexer() error {
	var pending bool
	if err := a.DB.QueryRowContext(a.ctx, "SELECT EXISTS(SELECT 1 FROM maintenance_jobs WHERE name IN(?,?) AND complete=0)", processingCardsJob, processingTimelineJob).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		for _, step := range []struct{ table, job string }{{"timeline_entries", processingTimelineJob}, {"event_cards", processingCardsJob}} {
			if err := a.runMaintenance(a.ctx, step.job, backfillProcessing(step.table), 10*time.Millisecond); err != nil {
				if a.ctx.Err() == nil {
					log.Printf("processing summary indexing: %v (checkpoint retained; restart to resume)", err)
				}
				return
			}
		}
	}()
	return nil
}
