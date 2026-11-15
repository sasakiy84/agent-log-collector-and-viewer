package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type SkillEvidence struct {
	Name     string `json:"name"`
	Plugin   string `json:"plugin,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Evidence string `json:"evidence"`
	Path     string `json:"-"`
}
type SkillSummary struct {
	Name       string   `json:"name"`
	Plugin     string   `json:"plugin,omitempty"`
	Sessions   int      `json:"sessions"`
	Uses       int      `json:"uses"`
	References int      `json:"references"`
	Last       string   `json:"last"`
	Scopes     []string `json:"scopes"`
	Event      string   `json:"event,omitempty"`
}

const skillReported = `CASE WHEN k.evidence='reported' AND (k.origin='otel' OR NOT EXISTS(SELECT 1 FROM skill_events native WHERE native.name=k.name AND native.plugin=k.plugin AND native.session=k.session AND native.evidence='reported' AND native.origin='otel')) THEN k.group_key END`
const skillRollup = `SELECT k.name,k.plugin,COUNT(DISTINCT k.session) AS sessions,COUNT(DISTINCT ` + skillReported + `) AS uses,COUNT(DISTINCT CASE WHEN k.evidence='reference' THEN k.session END) AS reference_sessions,MAX(k.time) AS last,json_group_array(DISTINCT k.scope) AS scopes`

func scanSkills(rows *sql.Rows) ([]SkillSummary, error) {
	return scanSkillRows(rows, nil)
}

func scanSkillRows(rows *sql.Rows, totalUses *int) ([]SkillSummary, error) {
	defer rows.Close()
	out := []SkillSummary{}
	for rows.Next() {
		var s SkillSummary
		var scopes string
		args := []any{&s.Name, &s.Plugin, &s.Sessions, &s.Uses, &s.References, &s.Last, &scopes}
		if totalUses != nil {
			args = append(args, totalUses)
		}
		if err := rows.Scan(args...); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopes), &s.Scopes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (a *App) skills(w http.ResponseWriter, r *http.Request) {
	order := "uses DESC,sessions DESC,last DESC,name,plugin"
	sort := r.URL.Query().Get("sort")
	switch sort {
	case "", "uses":
		sort = "uses"
	case "sessions":
		order = "sessions DESC,uses DESC,last DESC,name,plugin"
	case "recent":
		order = "last DESC,name,plugin"
	default:
		http.Error(w, "Invalid skill sort", 400)
		return
	}
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	from := ` FROM skill_events k JOIN session_catalog s ON s.session=k.session WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=k.session)`
	args := []any{}
	if r.URL.Query().Has("project") {
		from += " AND s.project_key=?"
		args = append(args, canonicalProjectID(r.URL.Query().Get("project")))
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if len(q) > 512 {
			http.Error(w, "Search is too long", 400)
			return
		}
		from += ` AND (k.name,k.plugin) IN(SELECT matched.name,matched.plugin FROM skill_events matched JOIN session_catalog matched_session ON matched_session.session=matched.session WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=matched.session) AND instr(matched.search,?)>0`
		args = append(args, skillSearch(q))
		if r.URL.Query().Has("project") {
			from += " AND matched_session.project_key=?"
			args = append(args, canonicalProjectID(r.URL.Query().Get("project")))
		}
		from += ")"
	}
	grouped := from + " GROUP BY k.name,k.plugin"
	var total int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM (SELECT k.name"+grouped+")", args...).Scan(&total); err != nil {
		databaseError(w, r, err)
		return
	}
	off := pageOffset(offset(r), total, 200)
	params := append(append([]any{}, args...), off)
	// Window aggregation covers every matching skill before LIMIT, so the
	// displayed total remains correct on later pages and under project/search filters.
	rows, err := tx.QueryContext(r.Context(), "WITH totals AS ("+skillRollup+grouped+") SELECT name,plugin,sessions,uses,reference_sessions,last,scopes,SUM(uses) OVER() FROM totals ORDER BY "+order+" LIMIT 200 OFFSET ?", params...)
	var out []SkillSummary
	var uses int
	if err == nil {
		out, err = scanSkillRows(rows, &uses)
	}
	var ready bool
	if err == nil {
		err = tx.QueryRowContext(r.Context(), "SELECT complete FROM maintenance_jobs WHERE name=?", skillsJob).Scan(&ready)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, map[string]any{"skills": out, "total": total, "uses": uses, "sort": sort, "offset": off, "ready": ready})
}
func sessionSkills(ctx context.Context, tx *sql.Tx, session, origin string) ([]SkillSummary, error) {
	from := ` FROM skill_events k WHERE k.session=?`
	args := []any{session}
	if origin != "" {
		from += " AND k.origin=?"
		args = append(args, origin)
	}
	rollup := skillRollup
	if origin != "" {
		rollup = strings.Replace(rollup, skillReported, "CASE WHEN k.evidence='reported' THEN k.group_key END", 1)
	}
	rows, err := tx.QueryContext(ctx, rollup+from+" GROUP BY k.name,k.plugin ORDER BY MAX(k.time) DESC,k.name,k.plugin", args...)
	if err != nil {
		return nil, err
	}
	out, err := scanSkills(rows)
	if err != nil {
		return nil, err
	}
	for i := range out {
		s := &out[i]
		query := `SELECT event_id FROM skill_events WHERE session=? AND name=? AND plugin=?`
		args := []any{session, s.Name, s.Plugin}
		if origin != "" {
			query += " AND origin=?"
			args = append(args, origin)
		}
		err = tx.QueryRowContext(ctx, query+" ORDER BY CASE WHEN evidence='reported' THEN 0 ELSE 1 END,time DESC,event_id LIMIT 1", args...).Scan(&s.Event)
		if err != nil {
			return nil, err
		}
		if origin == "" {
			var logical string
			err = tx.QueryRowContext(ctx, `SELECT id FROM timeline_entries WHERE id=? AND session=? UNION ALL SELECT id FROM timeline_entries WHERE otel_id=? AND import_id<>'' AND otel_id<>'' AND session=? LIMIT 1`, s.Event, session, s.Event, session).Scan(&logical)
			if err == nil {
				s.Event = logical
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
	}
	return out, nil
}
func attachSkills(ctx context.Context, tx *sql.Tx, events []TimelineEvent) error {
	ids := []string{}
	positions := map[string]int{}
	for i, e := range events {
		if e.Source != "codex" {
			continue
		}
		for _, record := range e.Records {
			ids = append(ids, record.ID)
			positions[record.ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(ids)
	rows, err := tx.QueryContext(ctx, `SELECT event_id,name,plugin,scope,mode,evidence,path FROM skill_events WHERE event_id IN(SELECT value FROM json_each(?)) ORDER BY name,plugin,evidence`, string(encoded))
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var f SkillEvidence
		if err = rows.Scan(&id, &f.Name, &f.Plugin, &f.Scope, &f.Mode, &f.Evidence, &f.Path); err != nil {
			return err
		}
		i := positions[id]
		key := fmt.Sprint(i) + "\x00" + f.Name + "\x00" + f.Plugin + "\x00" + f.Evidence
		if !seen[key] {
			events[i].Skills = append(events[i].Skills, f)
			seen[key] = true
		}
	}
	return rows.Err()
}
