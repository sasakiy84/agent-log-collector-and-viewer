package app

import (
	"context"
	"database/sql"
)

const toolTimingJob = "codex_tool_timing.v1"

// Run independently of the completed Guardian/conversation repair. Only native
// Codex tool spans need original payloads; imports and other providers stay out.
func backfillToolTiming(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+qualifiedColumns("e")+` FROM event_cards c CROSS JOIN events e ON e.id=c.id
 WHERE c.id>? AND c.source='codex' AND c.origin='otel' AND c.signal='traces' AND c.kind='tool' ORDER BY c.id LIMIT 128`, cursor)
	if err != nil {
		return cursor, 0, err
	}
	for _, e := range batch {
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			return cursor, 0, err
		}
		d := describe(e, doc)
		if d.Diagnostic {
			if d.Kind != "tool" {
				if err = removeToolIdentity(ctx, tx, e); err != nil {
					return cursor, 0, err
				}
			}
			if err = storeCard(ctx, tx, e, d, doc); err != nil {
				return cursor, 0, err
			}
		}
		cursor = e.ID
	}
	if err = refreshProjections(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, len(batch), nil
}

// Queue both originals before deleting a false correlation key. Otherwise a
// formerly merged imported result could retain the old native presentation.
func removeToolIdentity(ctx context.Context, tx *sql.Tx, e Event) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO read_model_dirty
 SELECT session,import_id FROM timeline_entries WHERE (import_id=? OR otel_id=?) AND import_id<>''
 UNION ALL SELECT session,otel_id FROM timeline_entries WHERE (import_id=? OR otel_id=?) AND otel_id<>''`, e.ID, e.ID, e.ID, e.ID)
	if err == nil {
		_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO read_model_dirty VALUES(?,?)", e.Session, e.ID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM event_keys WHERE event_id=?", e.ID)
	}
	return err
}
