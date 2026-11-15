package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
)

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
	if err := a.DB.QueryRow("SELECT complete FROM maintenance_jobs WHERE name=?", skillsJob).Scan(&complete); err != nil {
		return err
	}
	if complete {
		return nil
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		if err := a.runMaintenance(a.ctx, skillsJob, backfillSkills, 10*time.Millisecond); err != nil && a.ctx.Err() == nil {
			log.Printf("skill history indexing: %v (checkpoint retained; restart to resume)", err)
		}
	}()
	return nil
}
func backfillSkills(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,session,origin,source,time,signal,CASE WHEN source='codex' THEN detail ELSE NULL END FROM event_cards WHERE id>? ORDER BY id LIMIT 128`, cursor)
	if err != nil {
		return cursor, 0, err
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
		return cursor, 0, errors.Join(err, rowErr)
	}
	for _, v := range batch {
		if ctx.Err() != nil {
			return cursor, 0, ctx.Err()
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
		if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET has_skill=EXISTS(SELECT 1 FROM skill_events WHERE event_id=?) WHERE id=? AND has_skill<>EXISTS(SELECT 1 FROM skill_events WHERE event_id=?)", v.event.ID, v.event.ID, v.event.ID); err != nil {
			break
		}
	}

	if err != nil {
		return cursor, 0, err
	}
	if err = refreshPresentation(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, len(batch), nil
}
