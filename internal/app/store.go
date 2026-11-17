package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
)

func (a *App) Save(ctx context.Context, events []Event) error {
	if a.compacting.Load() {
		return fmt.Errorf("database compaction is in progress")
	}
	// Decode and tokenize before reserving SQLite's single writer. Database
	// publication remains atomic; malformed batches never publish partial facts.
	analyzed := make([]recordAnalysis, len(events))
	// Preflight is advisory. Recheck under the writer reservation: a concurrent
	// update/delete, or a preceding import with the same ID, may change the answer.
	reader := a.readers
	if reader == nil {
		reader = a.DB
	}
	preflight, err := reader.PrepareContext(ctx, unchangedRecordSQL)
	if err != nil {
		return err
	}
	defer preflight.Close()
	for i, event := range events {
		event = identifyRecord(event)
		idHash, rawHash := sha256.Sum256([]byte(event.ID)), sha256.Sum256(event.Raw)
		var unchanged, missingHash bool
		if err = preflight.QueryRowContext(ctx, event.ID, rawHash[:], idHash[:], rawHash[:], event.ID).Scan(&unchanged, &missingHash); err != nil {
			return err
		}
		if !unchanged && missingHash {
			if err = reader.QueryRowContext(ctx, exactRecordSQL, event.ID, []byte(event.Raw)).Scan(&unchanged); err != nil {
				return err
			}
		}
		if unchanged {
			analyzed[i].event = event
			continue
		}
		record, err := analyzeRecord(ctx, event)
		if err != nil {
			return err
		}
		analyzed[i] = record
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cache := &recordStatements{tx: tx, statements: map[string]*sql.Stmt{}}
	defer cache.close()
	ctx = context.WithValue(ctx, statementContextKey{}, cache)
	for i, record := range analyzed {
		if !record.ready {
			e := record.event
			idHash, rawHash := sha256.Sum256([]byte(e.ID)), sha256.Sum256(e.Raw)
			var unchanged, missingHash bool
			if err = queryRecord(ctx, tx, unchangedRecordSQL, e.ID, rawHash[:], idHash[:], rawHash[:], e.ID).Scan(&unchanged, &missingHash); err != nil {
				return err
			}
			if !unchanged && missingHash {
				if err = queryRecord(ctx, tx, exactRecordSQL, e.ID, []byte(e.Raw)).Scan(&unchanged); err != nil {
					return err
				}
			}
			if unchanged {
				continue
			}
			record, err = analyzeRecord(ctx, e)
			if err != nil {
				return err
			}
		}
		// Release parsed maps and token strings as soon as their record is stored.
		analyzed[i] = recordAnalysis{}

		e, doc, detail := record.event, record.document, record.detail
		idHash, rawHash := sha256.Sum256([]byte(e.ID)), sha256.Sum256(e.Raw)
		var archived bool
		if err = queryRecord(ctx, tx, "SELECT EXISTS(SELECT 1 FROM record_tombstones WHERE id_hash=? AND raw_hash=?)", idHash[:], rawHash[:]).Scan(&archived); err != nil {
			return err
		}
		if archived {
			continue
		}
		if legacy, ok := legacyUsageRaw(doc); ok {
			fingerprint := sha256.Sum256(legacy)
			if err = queryRecord(ctx, tx, `SELECT EXISTS(SELECT 1 FROM record_tombstones a WHERE id_hash=? AND raw_hash=?
 AND NOT EXISTS(SELECT 1 FROM usage_tombstones v WHERE v.id_hash=a.id_hash AND v.raw_hash=a.raw_hash))`, idHash[:], fingerprint[:]).Scan(&archived); err != nil {
				return err
			}
			if archived {
				continue
			}
		}
		result, err := execRecord(ctx, tx, "INSERT INTO records(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence,origin,kind,tool_name,raw_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session=excluded.session,parent=excluded.parent,source=excluded.source,signal=excluded.signal,kind=excluded.kind,tool_name=excluded.tool_name,time=excluded.time,name=excluded.name,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,raw=excluded.raw,raw_hash=excluded.raw_hash,sequence=excluded.sequence,origin=excluded.origin WHERE records.id LIKE 'import:%' AND records.raw IS NOT excluded.raw", e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, []byte(e.Raw), e.Sequence, e.Origin, detail.Kind, str(firstValue(detail.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe")), rawHash[:])
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
		if _, err = execRecord(ctx, tx, "DELETE FROM record_keys WHERE event_id=?", e.ID); err != nil {
			return err
		}
		if err = storeCard(ctx, tx, e, detail, doc); err != nil {
			return err
		}
		if err = storeSearchDocument(ctx, tx, e, record.search); err != nil {
			return err
		}
		for _, key := range record.keys {
			if _, err = execRecord(ctx, tx, "INSERT INTO record_keys(event_id,namespace,key) VALUES(?,?,?)", e.ID, key.Namespace, key.Key); err != nil {
				return err
			}
		}

	}
	if err = refreshProjections(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Import IDs are mutable; OTEL IDs identify immutable content. Tombstones use
// both hashes, so a changed imported record can legitimately be ingested again.
const unchangedRecordSQL = `SELECT EXISTS(SELECT 1 FROM records WHERE id=? AND
 (id NOT LIKE 'import:%' OR raw_hash=?)) OR EXISTS(
 SELECT 1 FROM record_tombstones WHERE id_hash=? AND raw_hash=?),
 EXISTS(SELECT 1 FROM records WHERE id=? AND raw_hash IS NULL)`

// An initial generation-2 database can predate hashes. Exact BLOB comparison
// avoids a startup backfill and still skips morphology; offline-converted and
// newly received records always take the small hash-only path above.
const exactRecordSQL = `SELECT EXISTS(SELECT 1 FROM records WHERE id=? AND raw=?)`
