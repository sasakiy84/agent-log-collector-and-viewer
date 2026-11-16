package app

import (
	"context"
	"database/sql"
	"errors"
)

// Ownership is unchanged: an explicit parent or one unambiguous conversation
// owns a trace. The durable queue narrows reconciliation to changed identities.
const traceOwner = `SELECT CASE WHEN COUNT(*)=1 THEN MIN(session) END FROM (
 SELECT DISTINCT session FROM events WHERE source=? AND trace=?
 AND session NOT LIKE 'trace:%' AND session NOT LIKE 'unassigned:%' LIMIT 2)`

const reconcileTrace = `UPDATE events AS e SET session=COALESCE((
 SELECT p.session FROM events p WHERE p.source=e.source AND p.trace=e.trace
 AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%'
 AND p.span=e.parent_span LIMIT 1),?)
 WHERE e.source=? AND e.trace=? AND e.trace<>'' AND e.session GLOB 'trace:*'
 AND (? IS NOT NULL OR EXISTS(SELECT 1 FROM events p WHERE p.source=e.source AND p.trace=e.trace
 AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%'
 AND p.span=e.parent_span))`

func reconcileTraceBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT d.source,d.trace FROM trace_dirty d
 WHERE NOT EXISTS(SELECT 1 FROM archive_locks)
 OR NOT EXISTS(SELECT 1 FROM events e JOIN archive_locks l ON l.session=e.session
 WHERE e.source=d.source AND e.trace=d.trace) LIMIT 200`)
	if err != nil {
		return 0, err
	}
	batch := [][2]string{}
	for rows.Next() {
		var key [2]string
		if err = rows.Scan(&key[0], &key[1]); err != nil {
			break
		}
		batch = append(batch, key)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return 0, errors.Join(err, rowErr)
	}
	for _, key := range batch {
		var pending bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events
 WHERE source=? AND trace=? AND trace<>'' AND session GLOB 'trace:*')`, key[0], key[1]).Scan(&pending); err != nil {
			return 0, err
		}
		// A streaming response may contain hundreds of thousands of spans in
		// one trace. Compute its fallback owner once, not once per pending span;
		// keep the parent lookup separate so events_trace can seek the exact span.
		// Already assigned spans need neither a trace-wide owner scan nor repair.
		if pending {
			var owner sql.NullString
			if err = tx.QueryRowContext(ctx, traceOwner, key[0], key[1]).Scan(&owner); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, reconcileTrace, owner, key[0], key[1], owner); err != nil {
				return 0, err
			}
		}
		// Reconciliation can enqueue the same identity via the update trigger.
		// All changes are visible in this transaction and have just been handled.
		if _, err = tx.ExecContext(ctx, "DELETE FROM trace_dirty WHERE source=? AND trace=?", key[0], key[1]); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}

func reconcileTraces(ctx context.Context, tx *sql.Tx) error {
	for {
		count, err := reconcileTraceBatch(ctx, tx)
		if err != nil || count == 0 {
			return err
		}
	}
}
