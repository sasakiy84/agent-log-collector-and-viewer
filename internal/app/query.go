package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

func (a *App) sessions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT session,MAX(source),MAX(parent),COUNT(*),MIN(time),MAX(time), (SELECT p.raw FROM events p WHERE p.session=events.session ORDER BY (instr(lower(p.raw), 'user_prompt')>0 OR instr(lower(p.raw),'gen_ai.user.message')>0 OR instr(lower(p.raw),'gen_ai.input.messages')>0) DESC,p.time,p.sequence,p.id LIMIT 1) FROM events WHERE (?='' OR source=?) AND (?='' OR session IN (SELECT session FROM events WHERE instr(lower(session||' '||name||' '||CAST(raw AS TEXT)),lower(?))>0)) GROUP BY session ORDER BY MAX(time) DESC LIMIT 201 OFFSET ?`, r.URL.Query().Get("source"), r.URL.Query().Get("source"), r.URL.Query().Get("q"), r.URL.Query().Get("q"), offset(r))
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	defer rows.Close()
	type session struct {
		ID      string          `json:"id"`
		Source  string          `json:"source"`
		Parent  string          `json:"parent"`
		Count   int             `json:"count"`
		Start   string          `json:"start"`
		End     string          `json:"end"`
		Preview json.RawMessage `json:"preview"`
	}
	out := []session{}
	for rows.Next() {
		var s session
		if err := rows.Scan(&s.ID, &s.Source, &s.Parent, &s.Count, &s.Start, &s.End, &s.Preview); err != nil {
			http.Error(w, "Database error", 500)
			return
		}
		out = append(out, s)
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
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	v, e := a.readEvents(r.URL.Query().Get("session"), 201, offset(r))
	if e != nil {
		http.Error(w, "Database error", 500)
		return
	}
	write(w, v)
}
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	v, e := a.readEvents(r.URL.Query().Get("session"), -1, 0)
	if e != nil {
		http.Error(w, "Database error", 500)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="session.json"`)
	write(w, v)
}

func (a *App) session(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var source, parent, start, end string
	var count int
	err := a.DB.QueryRow("SELECT MAX(source),MAX(parent),MIN(time),MAX(time),COUNT(*) FROM events WHERE session=? HAVING COUNT(*)>0", id).Scan(&source, &parent, &start, &end, &count)
	if err == sql.ErrNoRows {
		http.Error(w, "Session not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	var preview json.RawMessage
	if err = a.DB.QueryRow("SELECT raw FROM events WHERE session=? ORDER BY (instr(lower(raw), 'user_prompt')>0 OR instr(lower(raw),'gen_ai.user.message')>0 OR instr(lower(raw),'gen_ai.input.messages')>0) DESC,time,sequence,id LIMIT 1", id).Scan(&preview); err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	rows, err := a.DB.Query("SELECT DISTINCT session FROM events WHERE parent=? ORDER BY session", id)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	defer rows.Close()
	children := []string{}
	for rows.Next() {
		var child string
		if err = rows.Scan(&child); err != nil {
			http.Error(w, "Database error", 500)
			return
		}
		children = append(children, child)
	}
	write(w, map[string]any{"id": id, "source": source, "parent": parent, "start": start, "end": end, "count": count, "children": children, "preview": preview})
}
