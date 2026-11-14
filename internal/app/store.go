package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
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
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY, session TEXT NOT NULL, parent TEXT NOT NULL, source TEXT NOT NULL, signal TEXT NOT NULL, time TEXT NOT NULL, name TEXT NOT NULL, trace TEXT NOT NULL, span TEXT NOT NULL, parent_span TEXT NOT NULL, raw BLOB NOT NULL, sequence INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS events_session ON events(session,time);
 CREATE INDEX IF NOT EXISTS events_trace ON events(source,trace,span);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	a := &App{DB: db, token: token}
	return a, nil
}
func (a *App) Save(events []Event) error {
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
		_, err = tx.Exec("INSERT INTO events(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session=excluded.session,parent=excluded.parent,time=excluded.time,name=excluded.name,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,raw=excluded.raw,sequence=excluded.sequence WHERE events.id LIKE 'import:%' AND events.raw IS NOT excluded.raw", e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, []byte(e.Raw), e.Sequence)
		if err != nil {
			return err
		}
	}
	// Only explicit session attributes establish trace ownership; unresolved spans are reconciled across batches.
	_, err = tx.Exec(`UPDATE events AS e SET session=(SELECT p.session FROM events p WHERE p.source=e.source AND p.trace=e.trace AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%' AND (p.span=e.parent_span OR (SELECT COUNT(DISTINCT q.session) FROM events q WHERE q.source=e.source AND q.trace=e.trace AND q.session NOT LIKE 'trace:%' AND q.session NOT LIKE 'unassigned:%')=1) LIMIT 1) WHERE e.trace<>'' AND e.session LIKE 'trace:%' AND EXISTS(SELECT 1 FROM events p WHERE p.source=e.source AND p.trace=e.trace AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%' AND (p.span=e.parent_span OR (SELECT COUNT(DISTINCT q.session) FROM events q WHERE q.source=e.source AND q.trace=e.trace AND q.session NOT LIKE 'trace:%' AND q.session NOT LIKE 'unassigned:%')=1))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) readEvents(session string, limit, off int) ([]Event, error) {
	rows, err := a.DB.Query("SELECT id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence FROM events WHERE session=? ORDER BY time,sequence,id LIMIT ? OFFSET ?", session, limit, off)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Session, &e.Parent, &e.Source, &e.Signal, &e.Time, &e.Name, &e.Trace, &e.Span, &e.ParentSpan, &e.Raw, &e.Sequence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
