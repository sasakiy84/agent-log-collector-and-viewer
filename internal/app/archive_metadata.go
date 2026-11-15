package app

import (
	"context"
	"database/sql"
	"errors"
)

// Older sessions are rebuilt once in committed batches. Normal ingestion and
// deletion maintain byte/record counts without re-reading the stored history.
func backfillArchiveStats(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT session FROM archive_stats_pending LIMIT 128")
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
		if _, err = tx.ExecContext(ctx, "DELETE FROM archive_stats WHERE session=?", session); err != nil {
			return 0, err
		}
		// length(BLOB) uses SQLite's stored byte length; no JSON decoding is needed.
		if _, err = tx.ExecContext(ctx, `INSERT INTO archive_stats SELECT session,COUNT(*),SUM(length(raw)) FROM events WHERE session=? GROUP BY session`, session); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM archive_stats_pending WHERE session=?", session); err != nil {
			return 0, err
		}
	}
	return len(sessions), nil
}
