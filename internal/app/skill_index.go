package app

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"time"
)

func insertSkillFact(ctx context.Context, tx *sql.Tx, e Event, f SkillEvidence, group string) error {
	stamp := f.Time
	if !validEventTime(stamp) {
		stamp = e.Time
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO skill_events VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_id,name,plugin,evidence,group_key) DO UPDATE SET session=excluded.session,origin=excluded.origin,scope=excluded.scope,mode=excluded.mode,time=excluded.time,path=excluded.path,search=excluded.search`, e.ID, e.Session, e.Origin, f.Name, f.Plugin, f.Scope, f.Mode, f.Evidence, group, stamp, f.Path, skillSearch(f.Name+" "+f.Plugin+" "+f.Scope+" "+f.Path))
	return err
}
func storeSkillFacts(ctx context.Context, tx *sql.Tx, e Event, d Detail) error {
	if len(d.SkillFacts) == 0 && d.Kind != "tool" && d.Kind != "user" && d.Kind != "skill" {
		return nil
	}
	for _, table := range []string{"skill_events", "skill_read_inputs"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE event_id=?", e.ID); err != nil {
			return err
		}
	}
	for _, f := range extractedSkills(e, d) {
		turn := skillString(firstValue(d.Attributes, "turn.id", "turn_id"), 512)
		if f.Instance != "" {
			turn = "event:" + e.ID + ":" + f.Instance
		}
		if turn == "" || f.Evidence != "reported" {
			turn = "event:" + e.ID
		}
		if err := insertSkillFact(ctx, tx, e, f, identityKey(e.Session, "skill:"+f.Name, f.Plugin, turn)); err != nil {
			return err
		}
	}
	if (e.Source == "codex" || e.Source == "claude" || e.Source == "copilot") && !strings.HasPrefix(e.Session, "trace:") && !strings.HasPrefix(e.Session, "unassigned:") {
		return storeSkillReadInput(ctx, tx, e, d)
	}
	return nil
}
func (a *App) startSkillIndexer() error {
	var complete bool
	if err := a.DB.QueryRowContext(a.ctx, "SELECT complete FROM maintenance_jobs WHERE name=?", skillsJob).Scan(&complete); err != nil {
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

// Read originals in bounded batches so old clipped command cards cannot hide reads.
func backfillSkills(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+qualifiedColumns("e")+` FROM events e WHERE e.id>? AND e.source IN ('codex','claude','copilot') AND (e.kind IN ('tool','user','skill') OR e.source='copilot') ORDER BY e.id LIMIT 128`, cursor)
	if err != nil {
		return cursor, 0, err
	}
	for _, e := range batch {
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			return cursor, 0, err
		}
		if err = storeSkillFacts(ctx, tx, e, describe(e, doc)); err != nil {
			return cursor, 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET has_skill=EXISTS(SELECT 1 FROM skill_events WHERE event_id=?) WHERE id=? AND has_skill<>EXISTS(SELECT 1 FROM skill_events WHERE event_id=?)", e.ID, e.ID, e.ID); err != nil {
			return cursor, 0, err
		}
		cursor = e.ID
	}
	if err = refreshSkillReads(ctx, tx); err != nil {
		return cursor, 0, err
	}
	if err = refreshPresentation(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, len(batch), nil
}
