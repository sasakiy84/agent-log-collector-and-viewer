package app

import (
	"context"
	"database/sql"
)

func backfillAgents(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+qualifiedColumns("e")+" FROM event_cards c CROSS JOIN events e ON e.id=c.id WHERE c.id>? AND (c.source='copilot' OR json_extract(c.detail,'$.kind') IN ('tool','agent')) ORDER BY c.id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
	}
	for _, e := range batch {
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			return cursor, 0, err
		}
		// Raw identity metadata was deliberately omitted from bounded cards.
		if err = storeAgentData(ctx, tx, e, describe(e, doc)); err != nil {
			return cursor, 0, err
		}
		cursor = e.ID
	}
	return cursor, len(batch), nil
}
