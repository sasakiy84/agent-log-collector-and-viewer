package app

import (
	"context"
	"database/sql"
	"strings"
)

// A queued event already counts as activity even though its view is not yet
// published. Check only the selected sessions: bulk ingestion elsewhere must
// not block archiving unrelated histories or canceling/reclaiming existing jobs.
func (a *App) archiveHasPending(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	if a.inbox == nil {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT session FROM archive_sessions WHERE archive_id=?", id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	check := func(ids []any) (bool, error) {
		if len(ids) == 0 {
			return false, nil
		}
		var pending bool
		err := a.inbox.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ingest_sessions WHERE session IN ("+strings.TrimRight(strings.Repeat("?,", len(ids)), ",")+"))", ids...).Scan(&pending)
		return pending, err
	}
	ids := []any{}
	for rows.Next() {
		var session string
		if err = rows.Scan(&session); err != nil {
			return false, err
		}
		ids = append(ids, session)
		if len(ids) == 200 {
			pending, err := check(ids)
			if err != nil || pending {
				return pending, err
			}
			ids = ids[:0]
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	return check(ids)
}
