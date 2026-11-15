package app

import (
	"context"
	"database/sql"
	"strings"
)

const codexVisibilityJob = "codex_visibility.v1"

func storeSessionFacts(ctx context.Context, tx *sql.Tx, e Event, doc document) error {
	var ephemeral, persistent, temporary bool
	if e.Source == "codex" {
		objects := []map[string]any{doc.Resource, doc.Scope, doc.Record}
		for _, event := range arr(doc.Record["events"]) {
			objects = append(objects, obj(event))
		}
		for _, object := range objects {
			attrs := decodedAttributes(object["attributes"])
			switch attrs["session_init.ephemeral"] {
			case true, "true":
				ephemeral = true
			case false, "false":
				persistent = true
			}
			temporary = temporary || strings.HasPrefix(str(attrs["rpc.request_id"]), "temporary-structured-")
		}
	}
	if !ephemeral && !persistent && !temporary {
		_, err := tx.ExecContext(ctx, "DELETE FROM codex_session_facts WHERE event_id=?", e.ID)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO codex_session_facts VALUES(?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET
 session=excluded.session,ephemeral=excluded.ephemeral,persistent=excluded.persistent,temporary=excluded.temporary`,
		e.ID, e.Session, ephemeral, persistent, temporary)
	return err
}

func refreshSessionVisibility(ctx context.Context, tx *sql.Tx) error {
	for {
		count, err := refreshSessionVisibilityBatch(ctx, tx)
		if err != nil || count == 0 {
			return err
		}
	}
}

func refreshSessionVisibilityBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	// Evidence can arrive out of order, move during trace reconciliation, or be
	// replaced/deleted. Recompute only affected sessions, including reversals.
	_, err := tx.ExecContext(ctx, `WITH flags AS MATERIALIZED (
 SELECT d.session,COALESCE(MAX(f.ephemeral),0) AND COALESCE(MAX(f.temporary),0)
 AND NOT COALESCE(MAX(f.persistent),0) AS internal
 FROM (SELECT session FROM session_visibility_dirty LIMIT 200) d
 LEFT JOIN codex_session_facts f ON f.session=d.session GROUP BY d.session
 ) UPDATE session_catalog AS s SET internal=f.internal FROM flags f
 WHERE s.session=f.session AND s.internal<>f.internal`)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM session_visibility_dirty WHERE session IN (SELECT session FROM session_visibility_dirty LIMIT 200)")
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func backfillSessionVisibility(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	// Walk bounded primary-key pages once. Filtering by source before LIMIT
	// could scan/sort an unbounded nonmatching history in one transaction.
	columns := strings.Replace(eventColumns, "raw", "CASE WHEN source='codex' THEN raw ELSE X'' END", 1)
	batch, err := loadEventBatch(ctx, tx, "SELECT "+columns+" FROM events WHERE id>? ORDER BY id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
	}
	for _, e := range batch {
		if e.Source == "codex" {
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				return cursor, 0, err
			}
			if err = storeSessionFacts(ctx, tx, e, doc); err != nil {
				return cursor, 0, err
			}
		}
		cursor = e.ID
	}
	if _, err = refreshSessionVisibilityBatch(ctx, tx); err == nil {
		_, err = refreshFolderBatch(ctx, tx)
	}
	return cursor, len(batch), err
}
