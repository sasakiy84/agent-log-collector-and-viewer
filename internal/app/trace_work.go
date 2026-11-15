package app

import (
	"context"
	"database/sql"
	"errors"
)

// Ownership is unchanged: an explicit parent or one unambiguous conversation
// owns a trace. The durable queue narrows reconciliation to changed identities.
const reconcileTrace = `UPDATE events AS e SET session=(
 SELECT p.session FROM events p WHERE p.source=e.source AND p.trace=e.trace
 AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%'
 AND (p.span=e.parent_span OR (SELECT COUNT(DISTINCT q.session) FROM events q
 WHERE q.source=e.source AND q.trace=e.trace AND q.session NOT LIKE 'trace:%' AND q.session NOT LIKE 'unassigned:%')=1) LIMIT 1)
 WHERE e.source=? AND e.trace=? AND e.trace<>'' AND e.session GLOB 'trace:*'
 AND EXISTS(SELECT 1 FROM events p WHERE p.source=e.source AND p.trace=e.trace
 AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%'
 AND (p.span=e.parent_span OR (SELECT COUNT(DISTINCT q.session) FROM events q
 WHERE q.source=e.source AND q.trace=e.trace AND q.session NOT LIKE 'trace:%' AND q.session NOT LIKE 'unassigned:%')=1))`

func reconcileTraceBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT d.source,d.trace FROM trace_dirty d
 WHERE NOT EXISTS(SELECT 1 FROM events e JOIN archive_locks l ON l.session=e.session
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
		if _, err = tx.ExecContext(ctx, reconcileTrace, key[0], key[1]); err != nil {
			return 0, err
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
