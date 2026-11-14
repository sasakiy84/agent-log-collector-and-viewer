package app

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	query := `SELECT session,source,parent,count,start,end,preview FROM session_catalog s WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=s.session)`
	args := []any{}
	if source := r.URL.Query().Get("source"); source != "" {
		query += " AND source=?"
		args = append(args, source)
	}
	if search := r.URL.Query().Get("q"); search != "" {
		query += ` AND (instr(lower(s.session||' '||CAST(s.preview AS TEXT)),lower(?))>0 OR EXISTS(SELECT 1 FROM events e WHERE e.session=s.session AND instr(lower(e.name||' '||CAST(e.raw AS TEXT)),lower(?))>0))`
		args = append(args, search, search)
	}
	query += " ORDER BY end DESC,session LIMIT 201 OFFSET ?"
	args = append(args, offset(r))
	rows, err := a.readers.QueryContext(r.Context(), query, args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	out := []SessionSummary{}
	for rows.Next() {
		var summary SessionSummary
		var encoded []byte
		if err = rows.Scan(&summary.ID, &summary.Source, &summary.Parent, &summary.Count, &summary.Start, &summary.End, &encoded); err != nil {
			break
		}
		var detail Detail
		if err = json.Unmarshal(encoded, &detail); err != nil {
			break
		}
		summary.Preview = &detail
		out = append(out, summary)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		databaseError(w, r, errors.Join(err, rowErr))
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
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM archive_locks WHERE session=?)", id).Scan(&locked); err != nil {
		databaseError(w, r, err)
		return
	}
	if locked {
		http.Error(w, "Session is being archived", 409)
		return
	}
	columns := "count,start,end"
	if origin != "" {
		columns = origin + "_count," + origin + "_start," + origin + "_end"
	}
	page := SessionPage{SessionSummary: SessionSummary{ID: id}, Children: []string{}}
	var preview []byte
	err = tx.QueryRowContext(r.Context(), "SELECT source,parent,"+columns+",preview FROM session_catalog WHERE session=?", id).Scan(&page.Source, &page.Parent, &page.Count, &page.Start, &page.End, &preview)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	page.Events, err = readTimeline(r.Context(), tx, id, origin, 201, offset(r), false)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if origin == "" {
		var detail Detail
		if err = json.Unmarshal(preview, &detail); err == nil {
			page.Preview = &detail
		}
	} else {
		var rows []TimelineEvent
		rows, err = readTimeline(r.Context(), tx, id, origin, 1, 0, true)
		if len(rows) > 0 {
			detail := summaryDetail(rows[0].Detail)
			page.Preview = &detail
		}
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT session FROM session_catalog WHERE parent=? ORDER BY session", id)
	if err != nil {
		databaseError(w, r, err)
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
		databaseError(w, r, errors.Join(err, rowErr))
		return
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, page)
}

// Full attributes and original JSON are fetched only for the inspected card.
func (a *App) event(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	session, id := r.URL.Query().Get("session"), r.URL.Query().Get("id")
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	// A native card may gain an imported counterpart after the page was loaded.
	// Resolve its old raw ID to the current group without requiring a refresh.
	if origin == "" {
		var current string
		err = tx.QueryRowContext(r.Context(), `SELECT id FROM timeline_entries WHERE id=? AND session=?
   UNION ALL SELECT id FROM timeline_entries WHERE otel_id=? AND import_id<>'' AND otel_id<>'' AND session=? LIMIT 1`, id, session, id, session).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			databaseError(w, r, err)
			return
		}
		id = current
	}
	query := "SELECT " + qualifiedColumns("e") + " FROM events e WHERE e.session=? AND "
	args := []any{session}
	if origin != "" {
		query += "e.id=? AND e.origin=?"
		args = append(args, id, origin)
	} else {
		query += `e.id IN (SELECT import_id FROM timeline_entries WHERE session=? AND id=? UNION ALL SELECT otel_id FROM timeline_entries WHERE session=? AND id=?)`
		args = append(args, session, id, session, id)
	}
	rows, err := tx.QueryContext(r.Context(), query, args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	records := []Event{}
	for rows.Next() {
		e, eErr := scanEvent(rows)
		if eErr != nil {
			err = eErr
			break
		}
		records = append(records, e)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		databaseError(w, r, errors.Join(err, rowErr))
		return
	}
	if len(records) == 0 {
		http.NotFound(w, r)
		return
	}
	event, err := projectEvent(id, records)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, event)
}
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	out, err := a.readRecords(r.Context(), r.URL.Query().Get("session"), origin, -1, 0)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="session.json"`)
	write(w, out)
}
