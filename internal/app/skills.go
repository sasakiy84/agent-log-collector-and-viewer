package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type SkillEvidence struct {
	Instance string `json:"-"`
	Time     string `json:"-"`
	Name     string `json:"name"`
	Plugin   string `json:"plugin,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Evidence string `json:"evidence"`
	Path     string `json:"path,omitempty"`
}
type SkillSummary struct {
	Name       string   `json:"name"`
	Plugin     string   `json:"plugin,omitempty"`
	Sessions   int      `json:"sessions"`
	Uses       int      `json:"uses"`
	Reads      int      `json:"reads"`
	Pending    int      `json:"pending"`
	Failed     int      `json:"failed"`
	Injections int      `json:"injections"`
	Last       string   `json:"last"`
	Scopes     []string `json:"scopes"`
	Event      string   `json:"event,omitempty"`
}

// These counters describe separate evidence. Reads never imply invocations.
func skillRollup() string {
	reported := "CASE WHEN k.evidence='reported' THEN k.group_key END"
	count := func(evidence string) string {
		condition := ""
		if evidence == "pending" || evidence == "failed" {
			condition = " AND NOT EXISTS(SELECT 1 FROM skill_events confirmed WHERE confirmed.group_key=k.group_key AND confirmed.evidence='read')"
		}
		if evidence == "failed" {
			condition += " AND NOT EXISTS(SELECT 1 FROM skill_events waiting WHERE waiting.group_key=k.group_key AND waiting.evidence='pending')"
		}

		return "COUNT(DISTINCT CASE WHEN k.evidence='" + evidence + "'" + condition + " THEN k.group_key END)"
	}
	return `SELECT k.name,k.plugin,COUNT(DISTINCT k.session) AS sessions,COUNT(DISTINCT ` + reported + `) AS uses,` + count("read") + ` AS reads,` + count("pending") + ` AS pending,` + count("failed") + ` AS failed,` + count("injection") + ` AS injections,MAX(k.time) AS last,json_group_array(DISTINCT k.scope) AS scopes`
}

type skillUsageTotals struct{ Uses, Reads int }

func scanSkillRows(rows *sql.Rows, totals *skillUsageTotals, includeEvent bool) ([]SkillSummary, error) {
	defer rows.Close()
	out := []SkillSummary{}
	for rows.Next() {
		var s SkillSummary
		var scopes string
		args := []any{&s.Name, &s.Plugin, &s.Sessions, &s.Uses, &s.Reads, &s.Pending, &s.Failed, &s.Injections, &s.Last, &scopes}
		if totals != nil {
			args = append(args, &totals.Uses, &totals.Reads)
		}
		if includeEvent {
			args = append(args, &s.Event)
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
	order := "reads DESC,uses DESC,sessions DESC,last DESC,name,plugin"
	sort := r.URL.Query().Get("sort")
	switch sort {
	case "", "reads":
		sort = "reads"
	case "uses":
		order = "uses DESC,reads DESC,sessions DESC,last DESC,name,plugin"
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

	scope := parseSearchFacets(r.URL.Query())
	if scope.Err != nil {
		http.Error(w, scope.Err.Error(), 400)
		return
	}
	if len(scope.Kinds) > 0 {
		http.Error(w, "Event types are not supported by the skill catalog", 400)
		return
	}
	mode := r.URL.Query().Get("text_mode")
	if mode != "" && mode != "all" && mode != "any" {
		http.Error(w, "Invalid text match mode", 400)
		return
	}
	evidence, evidenceArgs := scope.evidenceSQL("e")
	cte := `WITH skill_events AS NOT MATERIALIZED (SELECT e.* FROM main.skill_events e WHERE 1` + evidence + `) `
	from := ` FROM skill_events k JOIN sessions s ON s.session=k.session WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=k.session)`
	sessionScope, args := scope.sessionSQL()
	from += sessionScope
	searchCondition := func(alias, text string, anyWord bool) (string, []any) {
		parts, values := []string{}, []any{}
		for _, word := range strings.Fields(skillSearch(text)) {
			parts = append(parts, "instr("+alias+".search,?)>0")
			values = append(values, word)
		}
		if len(parts) == 0 {
			return "", nil
		}
		join := " AND "
		if anyWord {
			join = " OR "
		}
		return "(" + strings.Join(parts, join) + ")", values
	}
	for _, field := range []string{"q", "exclude"} {
		text := strings.TrimSpace(r.URL.Query().Get(field))
		if len(text) > 512 {
			http.Error(w, "Search is too long", 400)
			return
		}
		if text == "" {
			continue
		}
		condition, terms := searchCondition("matched", text, mode == "any" || field == "exclude")
		if condition == "" {
			continue
		}
		operator := " IN"
		if field == "exclude" {
			operator = " NOT IN"
		}
		from += ` AND (k.name,k.plugin)` + operator + `(SELECT matched.name,matched.plugin FROM skill_events matched JOIN sessions s ON s.session=matched.session WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=matched.session) AND ` + condition
		args = append(args, terms...)
		from += sessionScope
		args = append(args, scopeArgs(scope)...)
		from += ")"
	}
	args = append(append([]any{}, evidenceArgs...), args...)
	grouped := from + " GROUP BY k.name,k.plugin"
	var total int
	if err = tx.QueryRowContext(r.Context(), cte+"SELECT COUNT(*) FROM (SELECT k.name"+grouped+")", args...).Scan(&total); err != nil {
		databaseError(w, r, err)
		return
	}
	off := pageOffset(offset(r), total, 200)
	params := append(append([]any{}, args...), off)
	// Window aggregation covers every matching skill before LIMIT, so the
	// displayed total remains correct on later pages and under project/search filters.
	rows, err := tx.QueryContext(r.Context(), strings.TrimSuffix(cte, " ")+", totals AS ("+skillRollup()+grouped+") SELECT name,plugin,sessions,uses,reads,pending,failed,injections,last,scopes,SUM(uses) OVER(),SUM(reads) OVER() FROM totals ORDER BY "+order+" LIMIT 200 OFFSET ?", params...)
	var out []SkillSummary
	var totals skillUsageTotals
	if err == nil {
		out, err = scanSkillRows(rows, &totals, false)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, map[string]any{"skills": out, "total": total, "uses": totals.Uses, "reads": totals.Reads, "sort": sort, "offset": off, "ready": true})
}
func sessionSkills(ctx context.Context, tx *sql.Tx, session, origin string) ([]SkillSummary, error) {
	cte := `WITH skill_events AS NOT MATERIALIZED (SELECT * FROM main.skill_events WHERE session=?`
	args := []any{session}
	if origin != "" {
		cte += " AND origin=?"
		args = append(args, origin)
	}
	cte += ") "
	// Rank the preferred evidence for every skill in one pass. Correlated
	// point lookups below preserve current logical IDs after import/OTEL merge,
	// without one or two extra round trips for every distinct skill.
	cte += ", summaries AS (" + skillRollup() + ` FROM skill_events k GROUP BY k.name,k.plugin),
 ranked AS (SELECT name,plugin,event_id,ROW_NUMBER() OVER (
 PARTITION BY name,plugin ORDER BY CASE WHEN evidence='reported' THEN 0 ELSE 1 END,time DESC,event_id) AS position FROM skill_events)
 SELECT s.name,s.plugin,s.sessions,s.uses,s.reads,s.pending,s.failed,s.injections,s.last,s.scopes,`
	event := "r.event_id"
	if origin == "" {
		event = `COALESCE((SELECT id FROM logical_events WHERE id=r.event_id AND session=?
 UNION ALL SELECT id FROM logical_events WHERE otel_id=r.event_id AND import_id<>'' AND otel_id<>'' AND session=? LIMIT 1),r.event_id)`
		args = append(args, session, session)
	}
	rows, err := tx.QueryContext(ctx, cte+event+` FROM summaries s JOIN ranked r ON r.name=s.name AND r.plugin=s.plugin AND r.position=1
 ORDER BY s.last DESC,s.name,s.plugin`, args...)
	if err != nil {
		return nil, err
	}
	return scanSkillRows(rows, nil, true)
}
func attachSkills(ctx context.Context, tx *sql.Tx, events []TimelineEvent) error {
	ids := []string{}
	positions := map[string]int{}
	for i, e := range events {
		if e.Source != "codex" && e.Source != "claude" && e.Source != "copilot" {
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

func scopeArgs(scope searchFacets) []any { _, args := scope.sessionSQL(); return args }
