package app

import (
	"context"
	"database/sql"
)

// One trace can gain a closer request, conflicting ancestry, or lose a bridge.
// Previously resolved relationships are not immutable. Requeue only usage in
// changed traces, then let modelRoot enforce session/origin/unique-nearest rules.
func refreshModelAncestry(ctx context.Context, tx *sql.Tx) error {
	for {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'models',f.id,'' FROM (SELECT source,trace FROM pending_model_trace LIMIT 200) d
 CROSS JOIN model_facts f ON f.source=d.source AND f.trace=d.trace
 WHERE f.role='usage' ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM work_items WHERE kind='model_trace' AND (scope,key) IN(SELECT source,trace FROM pending_model_trace
 WHERE (source,trace) IN (SELECT source,trace FROM pending_model_trace LIMIT 200))`)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
	}
}
