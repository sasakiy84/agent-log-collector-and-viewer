package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	_ "modernc.org/sqlite"
	"os"
)

func Open(path, token string) (*App, error) {
	if len(token) < 16 {
		return nil, fmt.Errorf("AGENTLOG_TOKEN must contain at least 16 characters")
	}
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA auto_vacuum=INCREMENTAL; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY, session TEXT NOT NULL, parent TEXT NOT NULL, source TEXT NOT NULL, signal TEXT NOT NULL, time TEXT NOT NULL, trace TEXT NOT NULL, span TEXT NOT NULL, parent_span TEXT NOT NULL, sequence INTEGER NOT NULL DEFAULT 0, origin TEXT NOT NULL, kind TEXT NOT NULL, tool_name TEXT NOT NULL, name TEXT NOT NULL, raw BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS event_keys(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE, namespace TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY(event_id,namespace,key));
 CREATE INDEX IF NOT EXISTS event_keys_key ON event_keys(key,event_id);
 CREATE INDEX IF NOT EXISTS events_session ON events(session,origin,time);
 CREATE INDEX IF NOT EXISTS events_trace ON events(source,trace,span);
 CREATE INDEX IF NOT EXISTS events_pending_trace ON events(source,trace,parent_span) WHERE trace<>'' AND session GLOB 'trace:*';`)
	if err != nil {
		db.Close()
		return nil, err
	}
	a := &App{DB: db, token: token}
	// Required views finish before HTTP starts; log each stage so a large
	// existing database is distinguishable from a silent or stalled process.
	steps := []struct {
		name string
		run  func() error
	}{
		{"archive schema", func() error { _, err := db.Exec(archiveSchema); return err }},
		{"search schema", a.initSearchSchema},
		{"session views", a.initReadModel},
		{"folder summaries", a.initDirectoryIndex},
		{"usage summaries", a.initUsage},
		{"subagent links", a.initSubagents},
		{"full-text search", a.initSearchIndex},
	}
	for _, step := range steps {
		log.Printf("Database initialization: %s", step.name)
		if err = step.run(); err != nil {
			err = fmt.Errorf("%s: %w", step.name, err)
			break
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = a.initReaders(path); err != nil {
		db.Close()
		return nil, err
	}
	if err = a.initArchives(path); err != nil {
		if a.cancel != nil {
			a.cancel()
		}
		a.readers.Close()
		db.Close()
		return nil, err
	}
	if err = a.startSkillIndexer(); err != nil {
		a.Close()
		return nil, err
	}
	return a, nil
}
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
	// Only explicit session attributes establish trace ownership; unresolved spans are reconciled across batches.
	_, err = tx.Exec(`UPDATE events AS e SET session=(SELECT p.session FROM events p WHERE p.source=e.source AND p.trace=e.trace AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%' AND (p.span=e.parent_span OR (SELECT COUNT(DISTINCT q.session) FROM events q WHERE q.source=e.source AND q.trace=e.trace AND q.session NOT LIKE 'trace:%' AND q.session NOT LIKE 'unassigned:%')=1) LIMIT 1) WHERE e.trace<>'' AND e.session GLOB 'trace:*' AND EXISTS(SELECT 1 FROM events p WHERE p.source=e.source AND p.trace=e.trace AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%' AND (p.span=e.parent_span OR (SELECT COUNT(DISTINCT q.session) FROM events q WHERE q.source=e.source AND q.trace=e.trace AND q.session NOT LIKE 'trace:%' AND q.session NOT LIKE 'unassigned:%')=1))`)
	if err != nil {
		return err
	}
	if err = refreshCards(context.Background(), tx); err != nil {
		return err
	}
	if err = refreshSearch(context.Background(), tx); err != nil {
		return err
	}
	if err = refreshTimelines(context.Background(), tx); err != nil {
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
