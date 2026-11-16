package app

import (
	"context"
	"database/sql"
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
	ids := []string{}
	for rows.Next() {
		var session string
		if err = rows.Scan(&session); err != nil {
			return false, err
		}
		ids = append(ids, session)
		if len(ids) == 200 {
			pending, err := a.inbox.hasSessions(ctx, ids)
			if err != nil || pending {
				return pending, err
			}
			ids = ids[:0]
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	return a.inbox.hasSessions(ctx, ids)
}
