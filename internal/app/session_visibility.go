package app

import (
	"context"
	"database/sql"
	"strings"
)

const codexVisibilityJob = "codex_visibility.v1"
const codexAutomaticJob = "codex_automatic_sessions.v1"

func storeSessionFacts(ctx context.Context, tx *sql.Tx, e Event, doc document, detail Detail) error {
	var ephemeral, persistent, temporary, automatic, human bool
	if e.Source == "codex" {
		automatic, human = codexPromptFacts(detail)
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
	if !ephemeral && !persistent && !temporary && !automatic && !human {
		_, err := tx.ExecContext(ctx, "DELETE FROM codex_session_facts WHERE event_id=?", e.ID)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO codex_session_facts(event_id,session,ephemeral,persistent,temporary,automatic,human) VALUES(?,?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET
 session=excluded.session,ephemeral=excluded.ephemeral,persistent=excluded.persistent,temporary=excluded.temporary,automatic=excluded.automatic,human=excluded.human`,
		e.ID, e.Session, ephemeral, persistent, temporary, automatic, human)
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
 SELECT d.session,(COALESCE(MAX(f.ephemeral),0) AND COALESCE(MAX(f.temporary),0)
 AND NOT COALESCE(MAX(f.persistent),0))
 OR (EXISTS(SELECT 1 FROM codex_session_facts a WHERE a.session=d.session AND a.automatic<>0)
 AND NOT EXISTS(SELECT 1 FROM codex_session_facts h WHERE h.session=d.session AND h.human<>0)) AS internal
 FROM (SELECT session FROM session_visibility_dirty LIMIT 200) d
 LEFT JOIN codex_session_facts f ON f.session=d.session AND (f.ephemeral<>0 OR f.persistent<>0 OR f.temporary<>0)
 GROUP BY d.session
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
			// The subsequent automatic-session job extracts user-message facts.
			if err = storeSessionFacts(ctx, tx, e, doc, Detail{}); err != nil {
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

func backfillCodexAutomaticSessions(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	// The existing user-card index bounds each page. Non-Codex payloads and tool
	// outputs are not decoded; completed checkpoints are never scanned again.
	columns := strings.Replace(qualifiedColumns("e"), "e.raw", "CASE WHEN c.source='codex' THEN e.raw ELSE X'' END", 1)
	batch, err := loadEventBatch(ctx, tx, "SELECT "+columns+" FROM event_cards c CROSS JOIN events e ON e.id=c.id WHERE c.is_user=1 AND c.id>? ORDER BY c.id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
	}
	titles := []Event{}
	for _, event := range batch {
		if event.Source != "codex" {
			continue
		}
		doc, err := decodeDocument(event.Raw)
		if err != nil {
			return cursor, 0, err
		}
		detail := describe(event, doc)
		if err = storeSessionFacts(ctx, tx, event, doc, detail); err != nil {
			return cursor, 0, err
		}
		if automatic, _ := codexPromptFacts(detail); automatic {
			titles = append(titles, event)
		}
	}
	// Share the existing title/catalog repair; originals and search are intact.
	if _, _, err = updatePromptTitles(ctx, tx, titles, ""); err != nil {
		return cursor, 0, err
	}
	// Leave visibility queued until every user-message page is extracted. A
	// real user message in a later page must prevent hiding a mixed session.
	if len(batch) > 0 {
		cursor = batch[len(batch)-1].ID
	}
	return cursor, len(batch), err
}
