package app

import (
	"context"
	"database/sql"
	"errors"
)

// Repair provider presentations once in resumable, bounded background batches.
// Inspect only card identities for other providers; never decode Codex history.
func backfillProviders(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,source FROM event_cards WHERE id>? ORDER BY id LIMIT 128", cursor)
	if err != nil {
		return cursor, 0, err
	}
	type identity struct{ id, source string }
	batch := []identity{}
	for rows.Next() {
		var v identity
		if err = rows.Scan(&v.id, &v.source); err != nil {
			break
		}
		batch = append(batch, v)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return cursor, 0, err
	}
	processed, size := 0, 0
	for _, v := range batch {
		if err = ctx.Err(); err != nil {
			return cursor, 0, err
		}
		cursor = v.id
		processed++
		if v.source != "claude" && v.source != "copilot" {
			continue
		}
		e, err := scanEvent(tx.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM events WHERE id=?", v.id))
		if err != nil {
			return cursor, 0, err
		}
		size += len(e.Raw)
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			return cursor, 0, err
		}
		d := describe(e, doc)
		// Mark old pair members before replacing correlation keys, so removed links
		// split both originals instead of leaving a stale combined timeline card.
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO read_model_dirty VALUES(?,?);
 INSERT OR IGNORE INTO read_model_dirty SELECT session,import_id FROM timeline_entries WHERE (import_id=? OR otel_id=?) AND import_id<>'';
 INSERT OR IGNORE INTO read_model_dirty SELECT session,otel_id FROM timeline_entries WHERE (import_id=? OR otel_id=?) AND otel_id<>'';`, e.Session, e.ID, e.ID, e.ID, e.ID, e.ID)
		if err != nil {
			return cursor, 0, err
		}
		for _, table := range []string{"skill_events", "agent_calls", "agent_activity", "event_keys"} {
			if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE event_id=?", e.ID); err != nil {
				return cursor, 0, err
			}
		}
		for _, key := range correlationKeys(e, d, doc) {
			if _, err = tx.ExecContext(ctx, "INSERT INTO event_keys(event_id,namespace,key) VALUES(?,?,?)", e.ID, key.Namespace, key.Key); err != nil {
				return cursor, 0, err
			}
		}
		if err = storeCard(ctx, tx, e, d, doc); err != nil {
			return cursor, 0, err
		}
		if size >= 8<<20 {
			break
		}
	}
	if err = refreshProjections(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, processed, nil
}
