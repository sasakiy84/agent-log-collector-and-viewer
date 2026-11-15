package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
)

const skillSchema = `
CREATE TABLE IF NOT EXISTS skill_events(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 name TEXT NOT NULL,plugin TEXT NOT NULL,scope TEXT NOT NULL,mode TEXT NOT NULL,evidence TEXT NOT NULL,group_key TEXT NOT NULL,time TEXT NOT NULL,path TEXT NOT NULL,search TEXT NOT NULL,
 PRIMARY KEY(event_id,name,plugin,evidence));
CREATE INDEX IF NOT EXISTS skill_sessions ON skill_events(name,plugin,session,evidence,origin);
CREATE INDEX IF NOT EXISTS skills_by_session ON skill_events(session,name,plugin,time);
CREATE TRIGGER IF NOT EXISTS skill_event_update AFTER UPDATE ON events BEGIN DELETE FROM skill_events WHERE event_id=OLD.id; END;
CREATE TABLE IF NOT EXISTS skill_index_progress(id INTEGER PRIMARY KEY CHECK(id=1),cursor TEXT NOT NULL,complete INTEGER NOT NULL);
INSERT INTO skill_index_progress VALUES(1,'',0) ON CONFLICT DO NOTHING;`

func storeSkillFacts(ctx context.Context, tx *sql.Tx, e Event, d Detail) error {
	for _, f := range extractedSkills(e, d) {
		turn := skillString(firstValue(d.Attributes, "turn.id", "turn_id"), 512)
		if turn == "" {
			turn = "event:" + e.ID
		}
		group := identityKey(e.Session, "skill:"+f.Name, f.Plugin, turn)
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_events VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_id,name,plugin,evidence) DO UPDATE SET session=excluded.session,origin=excluded.origin,scope=excluded.scope,mode=excluded.mode,group_key=excluded.group_key,time=excluded.time,path=excluded.path,search=excluded.search`, e.ID, e.Session, e.Origin, f.Name, f.Plugin, f.Scope, f.Mode, f.Evidence, group, e.Time, f.Path, skillSearch(f.Name+" "+f.Plugin+" "+f.Scope+" "+f.Path))
		if err != nil {
			return err
		}
	}
	return nil
}
func (a *App) startSkillIndexer() error {
	var complete bool
	if err := a.DB.QueryRow("SELECT complete FROM skill_index_progress WHERE id=1").Scan(&complete); err != nil {
		return err
	}
	if complete {
		return nil
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		if err := a.indexSkills(a.ctx); err != nil && a.ctx.Err() == nil {
			log.Printf("skill history indexing: %v", err)
		}
	}()
	return nil
}
func (a *App) indexSkills(ctx context.Context) error {
	log.Print("Skill history indexing started in background")
	lastReport := time.Now()
	processed := 0
	for {
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var cursor string
		var complete bool
		if err = tx.QueryRowContext(ctx, "SELECT cursor,complete FROM skill_index_progress WHERE id=1").Scan(&cursor, &complete); err != nil {
			tx.Rollback()
			return err
		}
		if complete {
			tx.Rollback()
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,session,origin,source,time,signal,CASE WHEN source='codex' THEN detail ELSE NULL END FROM event_cards WHERE id>? ORDER BY id LIMIT 128`, cursor)
		if err != nil {
			tx.Rollback()
			return err
		}
		type item struct {
			event  Event
			detail []byte
		}
		batch := []item{}
		for rows.Next() {
			var v item
			if err = rows.Scan(&v.event.ID, &v.event.Session, &v.event.Origin, &v.event.Source, &v.event.Time, &v.event.Signal, &v.detail); err != nil {
				break
			}
			batch = append(batch, v)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			tx.Rollback()
			return errors.Join(err, rowErr)
		}
		for _, v := range batch {
			if ctx.Err() != nil {
				tx.Rollback()
				return ctx.Err()
			}
			cursor = v.event.ID
			if v.event.Source != "codex" {
				continue
			}
			var d Detail
			if err = json.Unmarshal(v.detail, &d); err != nil {
				break
			}
			if d.Name == "codex.skill_invocation" {
				var raw []byte
				err = tx.QueryRowContext(ctx, "SELECT raw FROM events WHERE id=?", v.event.ID).Scan(&raw)
				if err != nil {
					break
				}
				doc, eErr := decodeDocument(raw)
				if eErr != nil {
					err = eErr
					break
				}
				d = describe(v.event, doc)
				encoded, eErr := json.Marshal(cardDetail(d))
				if eErr != nil {
					err = eErr
					break
				}
				_, err = tx.ExecContext(ctx, "UPDATE event_cards SET detail=? WHERE id=?", encoded, v.event.ID)
				if err != nil {
					break
				}
			}
			if err = storeSkillFacts(ctx, tx, v.event, d); err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE skill_index_progress SET cursor=?,complete=? WHERE id=1", cursor, len(batch) == 0)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		processed += len(batch)
		if len(batch) == 0 {
			log.Printf("Skill history ready (%d cached records checked)", processed)
			return nil
		}
		if time.Since(lastReport) >= 5*time.Second {
			log.Printf("Skill history: %d cached records checked", processed)
			lastReport = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
