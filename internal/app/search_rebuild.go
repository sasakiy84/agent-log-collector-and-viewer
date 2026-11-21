package app

import (
	"context"
	"database/sql"
)

func rebuildSearchContent(ctx context.Context, tx *sql.Tx) error {
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM work_items WHERE kind='search_content_rebuild')").Scan(&pending); err != nil || !pending {
		return err
	}
	// WAL readers keep the previous index until the rebuild commits. Bound
	// the amount of original content decoded at a time with the batch loader.
	if _, err := tx.ExecContext(ctx, "INSERT INTO event_search(event_search) VALUES('delete-all')"); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, "INSERT INTO event_search(rowid,text) SELECT rowid,? FROM search_documents WHERE event_id=?")
	if err != nil {
		return err
	}
	defer insert.Close()
	after := ""
	for {
		batch, err := loadEventBatch(ctx, tx, "SELECT "+eventColumns+" FROM records WHERE id>? ORDER BY id LIMIT 200", after)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, event := range batch {
			doc, err := decodeDocument(event.Raw)
			if err != nil {
				return err
			}
			text, err := analyzeSearchText(ctx, event, doc, describe(event, doc))
			if err != nil {
				return err
			}
			if text != "" {
				if _, err := insert.ExecContext(ctx, text, event.ID); err != nil {
					return err
				}
			}
		}
		after = batch[len(batch)-1].ID
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM work_items WHERE kind='search_content_rebuild'")
	return err
}
