package app

import (
	"context"
	"database/sql"
	"strings"
)

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
		_, err := tx.ExecContext(ctx, "DELETE FROM session_evidence WHERE event_id=?", e.ID)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO session_evidence(event_id,session,ephemeral,persistent,temporary,automatic,human) VALUES(?,?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET
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
 OR (EXISTS(SELECT 1 FROM session_evidence a WHERE a.session=d.session AND a.automatic<>0)
 AND NOT EXISTS(SELECT 1 FROM session_evidence h WHERE h.session=d.session AND h.human<>0)) AS internal
 FROM (SELECT session FROM pending_visibility LIMIT 200) d
 LEFT JOIN session_evidence f ON f.session=d.session AND (f.ephemeral<>0 OR f.persistent<>0 OR f.temporary<>0)
 GROUP BY d.session
 ) UPDATE sessions AS s SET internal=f.internal FROM flags f
 WHERE s.session=f.session AND s.internal<>f.internal`)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM work_items WHERE kind='visibility' AND (scope,key) IN(SELECT session,'' FROM pending_visibility WHERE session IN (SELECT session FROM pending_visibility LIMIT 200))")
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}
