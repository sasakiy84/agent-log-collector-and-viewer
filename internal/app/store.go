package app

import (
	"context"
	"crypto/sha256"
	"fmt"
)

func (a *App) Save(ctx context.Context, events []Event) error {
	if a.compacting.Load() {
		return fmt.Errorf("database compaction is in progress")
	}
	// Decode and tokenize before reserving SQLite's single writer. Database
	// publication remains atomic; malformed batches never publish partial facts.
	analyzed := make([]recordAnalysis, 0, len(events))
	for _, event := range events {
		record, err := analyzeRecord(ctx, event)
		if err != nil {
			return err
		}
		analyzed = append(analyzed, record)
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range analyzed {
		e, doc, detail := record.event, record.document, record.detail
		idHash, rawHash := sha256.Sum256([]byte(e.ID)), sha256.Sum256(e.Raw)
		var archived bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM record_tombstones WHERE id_hash=? AND raw_hash=?)", idHash[:], rawHash[:]).Scan(&archived); err != nil {
			return err
		}
		if archived {
			continue
		}
		if legacy, ok := legacyUsageRaw(doc); ok {
			fingerprint := sha256.Sum256(legacy)
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM record_tombstones a WHERE id_hash=? AND raw_hash=?
 AND NOT EXISTS(SELECT 1 FROM usage_tombstones v WHERE v.id_hash=a.id_hash AND v.raw_hash=a.raw_hash))`, idHash[:], fingerprint[:]).Scan(&archived); err != nil {
				return err
			}
			if archived {
				continue
			}
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO records(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence,origin,kind,tool_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session=excluded.session,parent=excluded.parent,source=excluded.source,signal=excluded.signal,kind=excluded.kind,tool_name=excluded.tool_name,time=excluded.time,name=excluded.name,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,raw=excluded.raw,sequence=excluded.sequence,origin=excluded.origin WHERE records.id LIKE 'import:%' AND records.raw IS NOT excluded.raw", e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, []byte(e.Raw), e.Sequence, e.Origin, detail.Kind, str(firstValue(detail.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe")))
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM record_keys WHERE event_id=?", e.ID); err != nil {
			return err
		}
		if err = storeCard(ctx, tx, e, detail, doc); err != nil {
			return err
		}
		if err = storeSearchDocument(ctx, tx, e, record.search); err != nil {
			return err
		}
		for _, key := range record.keys {
			if _, err = tx.ExecContext(ctx, "INSERT INTO record_keys(event_id,namespace,key) VALUES(?,?,?)", e.ID, key.Namespace, key.Key); err != nil {
				return err
			}
		}

	}
	if err = refreshProjections(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
