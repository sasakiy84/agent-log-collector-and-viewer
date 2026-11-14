package app

import (
	"database/sql"
	"net/http"
	"strconv"
)

type SessionSummary struct {
	ID      string  `json:"id"`
	Source  string  `json:"source"`
	Parent  string  `json:"parent"`
	Count   int     `json:"count"`
	Start   string  `json:"start"`
	End     string  `json:"end"`
	Preview *Detail `json:"preview"`
}
type SessionPage struct {
	SessionSummary
	Children []string        `json:"children"`
	Events   []TimelineEvent `json:"events"`
}

func (a *App) sessions(w http.ResponseWriter, r *http.Request) {
	tx, err := a.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	defer tx.Rollback()
	args := append(projectionArgs("", ""), r.URL.Query().Get("source"), r.URL.Query().Get("source"), r.URL.Query().Get("q"), r.URL.Query().Get("q"), offset(r))
	rows, err := tx.QueryContext(r.Context(), timelineCTE+`, summaries AS (
 SELECT session,MAX(source) AS source,MAX(parent) AS parent,COUNT(*) AS count,MIN(time) AS start,MAX(end) AS end
 FROM timeline t WHERE (?='' OR source=?) AND (?='' OR session IN
 (SELECT m.session FROM members m JOIN events e ON e.id=m.id WHERE instr(lower(m.session||' '||e.name||' '||CAST(e.raw AS TEXT)),lower(?))>0))
 GROUP BY session ORDER BY MAX(end) DESC,session LIMIT 201 OFFSET ?
 ), previews AS (
 SELECT s.*,(SELECT id FROM timeline t WHERE t.session=s.session ORDER BY is_user DESC,time,sequence,id LIMIT 1) AS preview_id FROM summaries s
 ) SELECT p.session,p.source,p.parent,p.count,p.start,p.end,p.preview_id,`+qualifiedColumns("r")+`
 FROM previews p JOIN members m ON m.view_id=p.preview_id JOIN events r ON r.id=m.id ORDER BY p.end DESC,p.session,m.origin,m.id`, args...)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	type group struct {
		summary SessionSummary
		viewID  string
		records []Event
	}
	groups := []group{}
	for rows.Next() {
		var s SessionSummary
		var id string
		var e Event
		columns := []any{&s.ID, &s.Source, &s.Parent, &s.Count, &s.Start, &s.End, &id}
		if err = rows.Scan(append(columns, e.scanArgs()...)...); err != nil {
			break
		}
		if len(groups) == 0 || groups[len(groups)-1].summary.ID != s.ID {
			groups = append(groups, group{summary: s, viewID: id})
		}
		groups[len(groups)-1].records = append(groups[len(groups)-1].records, e)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		http.Error(w, "Database error", 500)
		return
	}
	out := make([]SessionSummary, 0, len(groups))
	for _, g := range groups {
		preview, err := projectEvent(g.viewID, g.records)
		if err != nil {
			http.Error(w, "Database error", 500)
			return
		}
		g.summary.Preview = &preview.Detail
		out = append(out, g.summary)
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	write(w, out)
}
func offset(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if n < 0 {
		return 0
	}
	return n
}
func eventOrigin(r *http.Request) (string, bool) {
	origin := r.URL.Query().Get("origin")
	return origin, origin == "" || origin == "import" || origin == "otel"
}
func (a *App) session(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	id := r.URL.Query().Get("id")
	tx, err := a.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	defer tx.Rollback()
	var page SessionPage
	page.ID = id
	page.Children = []string{}
	err = tx.QueryRowContext(r.Context(), "SELECT MAX(source),MAX(parent) FROM events WHERE session=? HAVING COUNT(*)>0", id).Scan(&page.Source, &page.Parent)
	if err == sql.ErrNoRows {
		http.Error(w, "Session not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	var start, end sql.NullString
	err = tx.QueryRowContext(r.Context(), timelineCTE+"SELECT COUNT(*),MIN(time),MAX(end) FROM timeline", projectionArgs(origin, id)...).Scan(&page.Count, &start, &end)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	page.Start = start.String
	page.End = end.String
	page.Events, err = readProjection(r.Context(), tx, id, origin, 201, offset(r), false)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	preview, err := readProjection(r.Context(), tx, id, origin, 1, 0, true)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	if len(preview) > 0 {
		page.Preview = &preview[0].Detail
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT DISTINCT session FROM events WHERE parent=? ORDER BY session", id)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	for rows.Next() {
		var child string
		if err = rows.Scan(&child); err != nil {
			break
		}
		page.Children = append(page.Children, child)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		http.Error(w, "Database error", 500)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	write(w, page)
}
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	out, err := a.readRecords(r.Context(), r.URL.Query().Get("session"), origin, -1, 0)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="session.json"`)
	write(w, out)
}
