package app

import (
	"context"
	"database/sql"
	"strings"
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
