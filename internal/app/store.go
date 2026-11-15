package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func (a *App) Save(events []Event) error {
	if a.compacting.Load() {
		return fmt.Errorf("database compaction is in progress")
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		if e.ID == "" {
			sum := sha256.Sum256(append([]byte(e.Source+e.Signal), e.Raw...))
			e.ID = hex.EncodeToString(sum[:])
		}
		idHash, rawHash := sha256.Sum256([]byte(e.ID)), sha256.Sum256(e.Raw)
		var archived bool
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM archived_records WHERE id_hash=? AND raw_hash=?)", idHash[:], rawHash[:]).Scan(&archived); err != nil {
			return err
		}
		if archived {
			continue
		}
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			return err
		}
		if legacy, ok := legacyUsageRaw(doc); ok {
			fingerprint := sha256.Sum256(legacy)
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM archived_records a WHERE id_hash=? AND raw_hash=?
 AND NOT EXISTS(SELECT 1 FROM archived_usage_records v WHERE v.id_hash=a.id_hash AND v.raw_hash=a.raw_hash))`, idHash[:], fingerprint[:]).Scan(&archived); err != nil {
				return err
			}
			if archived {
				continue
			}
		}
		detail := describe(e, doc)
		e.Origin = "otel"
		if detail.Attributes["agentlog.origin"] == "import" {
			e.Origin = "import"
		}
		result, err := tx.Exec("INSERT INTO events(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence,origin,kind,tool_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session=excluded.session,parent=excluded.parent,source=excluded.source,signal=excluded.signal,kind=excluded.kind,tool_name=excluded.tool_name,time=excluded.time,name=excluded.name,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,raw=excluded.raw,sequence=excluded.sequence,origin=excluded.origin WHERE events.id LIKE 'import:%' AND events.raw IS NOT excluded.raw", e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, []byte(e.Raw), e.Sequence, e.Origin, detail.Kind, str(firstValue(detail.Attributes, "gen_ai.tool.name", "tool_name", "tool_name_safe")))
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
		if _, err = tx.Exec("DELETE FROM event_keys WHERE event_id=?", e.ID); err != nil {
			return err
		}
		if err = storeCard(context.Background(), tx, e, detail, doc); err != nil {
			return err
		}
		if err = storeSearch(context.Background(), tx, e, doc); err != nil {
			return err
		}
		for _, key := range correlationKeys(e, detail, doc) {
			if _, err = tx.Exec("INSERT INTO event_keys(event_id,namespace,key) VALUES(?,?,?)", e.ID, key.Namespace, key.Key); err != nil {
				return err
			}
		}

	}
	if err = refreshProjections(context.Background(), tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) readRecords(ctx context.Context, session, origin string, limit, off int) ([]Event, error) {
	rows, err := a.readers.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE session=? AND (?='' OR origin=?) ORDER BY time,sequence,id LIMIT ? OFFSET ?", session, origin, origin, limit, off)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
