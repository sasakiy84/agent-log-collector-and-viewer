package app

import (
	"context"
	"database/sql"
)

// Dirty queues are their own checkpoints: derived writes and queue removal
// commit together. Bound both row count (in SQL) and original payload memory.
func loadEventBatch(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]Event, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batch, size := []Event{}, 0
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		batch = append(batch, event)
		size += len(event.Raw)
		if size >= 8<<20 {
			break
		}
	}
	return batch, rows.Err()
}
