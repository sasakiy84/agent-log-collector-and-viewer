package app

import (
	"context"
	"database/sql"
	"errors"
)

// Explicit record-total invalidations are repaired in bounded batches. Normal
// ingestion and deletion maintain these counters without history scans.
func refreshRecordTotalsBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT session FROM pending_storage LIMIT 128")
	if err != nil {
		return 0, err
	}
	sessions := []string{}
	for rows.Next() {
		var session string
		if err = rows.Scan(&session); err != nil {
			break
		}
		sessions = append(sessions, session)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	for _, session := range sessions {
		if _, err = tx.ExecContext(ctx, "DELETE FROM record_totals WHERE session=?", session); err != nil {
			return 0, err
		}
		// length(BLOB) uses SQLite's stored byte length; no JSON decoding is needed.
		if _, err = tx.ExecContext(ctx, `INSERT INTO record_totals SELECT session,COUNT(*),SUM(length(raw)) FROM records WHERE session=? GROUP BY session`, session); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM work_items WHERE kind='storage' AND (scope,key) IN(SELECT session,'' FROM pending_storage WHERE session=?)", session); err != nil {
			return 0, err
		}
	}
	return len(sessions), nil
}

func refreshRecordTotals(ctx context.Context, tx *sql.Tx) error {
	for {
		count, err := refreshRecordTotalsBatch(ctx, tx)
		if err != nil || count == 0 {
			return err
		}
	}
}
