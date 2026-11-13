package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	_ "modernc.org/sqlite"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed static/*
var assets embed.FS

type Event struct {
	ID         string          `json:"id"`
	Session    string          `json:"session"`
	Parent     string          `json:"parent,omitempty"`
	Source     string          `json:"source"`
	Signal     string          `json:"signal"`
	Time       string          `json:"time"`
	Name       string          `json:"name"`
	Trace      string          `json:"trace,omitempty"`
	Span       string          `json:"span,omitempty"`
	ParentSpan string          `json:"parent_span,omitempty"`
	Raw        json.RawMessage `json:"raw"`
}
type App struct {
	DB        *sql.DB
	token     string
	retention int
}

func Open(path, token string, retention int) (*App, error) {
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
 CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY, session TEXT NOT NULL, parent TEXT NOT NULL, source TEXT NOT NULL, signal TEXT NOT NULL, time TEXT NOT NULL, name TEXT NOT NULL, trace TEXT NOT NULL, span TEXT NOT NULL, parent_span TEXT NOT NULL, raw BLOB NOT NULL, received INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS events_session ON events(session,time);
 CREATE INDEX IF NOT EXISTS events_trace ON events(source,trace,span);
 CREATE INDEX IF NOT EXISTS events_received ON events(received);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	a := &App{db, token, retention}
	return a, nil
}
func (a *App) Prune() error {
	if a.retention <= 0 {
		return nil
	}
	_, e := a.DB.Exec("DELETE FROM events WHERE received < ?", time.Now().Add(-time.Duration(a.retention)*24*time.Hour).Unix())
	return e
}
func (a *App) Save(events []Event) error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		sum := sha256.Sum256(append([]byte(e.Source+e.Signal), e.Raw...))
		e.ID = hex.EncodeToString(sum[:])
		_, err = tx.Exec("INSERT OR IGNORE INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", e.ID, e.Session, e.Parent, e.Source, e.Signal, e.Time, e.Name, e.Trace, e.Span, e.ParentSpan, []byte(e.Raw), time.Now().Unix())
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
func (a *App) signature(v string) string {
	h := hmac.New(sha256.New, []byte(a.token))
	h.Write([]byte(v))
	return hex.EncodeToString(h.Sum(nil))
}
func (a *App) bearer(r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.token)) == 1
}
func (a *App) authenticated(r *http.Request) bool {
	if a.bearer(r) {
		return true
	}
	c, e := r.Cookie("agentlog")
	if e != nil {
		return false
	}
	p := strings.Split(c.Value, ".")
	if len(p) != 2 {
		return false
	}
	n, e := strconv.ParseInt(p[0], 10, 64)
	return e == nil && n > time.Now().Unix() && hmac.Equal([]byte(p[1]), []byte(a.signature(p[0])))
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, signal := range []string{"logs", "traces", "metrics"} {
		mux.HandleFunc("POST /v1/"+signal, func(w http.ResponseWriter, r *http.Request) {
			if !a.bearer(r) {
				http.Error(w, "Unauthorized", 401)
				return
			}
			a.receive(w, r)
		})
	}
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var v struct{ Token string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&v) != nil || subtle.ConstantTimeCompare([]byte(v.Token), []byte(a.token)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		exp := strconv.FormatInt(time.Now().Add(12*time.Hour).Unix(), 10)
		http.SetCookie(w, &http.Cookie{Name: "agentlog", Value: exp + "." + a.signature(exp), Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		write(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "agentlog", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		write(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/sessions", a.sessions)
	mux.HandleFunc("GET /api/events", a.events)
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("GET /api/export", a.export)
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		var n, s int
		if err := a.DB.QueryRow("SELECT COUNT(*),COUNT(DISTINCT session) FROM events").Scan(&n, &s); err != nil {
			http.Error(w, "Database error", 500)
			return
		}
		write(w, map[string]int{"events": n, "sessions": s})
	})
	sub, _ := fs.Sub(assets, "static")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/login" && !a.authenticated(r) {
			http.Error(w, "Unauthorized", 401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) sessions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT session,MAX(source),MAX(parent),COUNT(*),MIN(time),MAX(time), (SELECT p.raw FROM events p WHERE p.session=events.session ORDER BY (instr(lower(p.raw), 'prompt')>0) DESC,p.time,p.id LIMIT 1) FROM events WHERE (?='' OR source=?) AND (?='' OR session IN (SELECT session FROM events WHERE instr(lower(session||' '||name||' '||CAST(raw AS TEXT)),lower(?))>0)) GROUP BY session ORDER BY MAX(time) DESC LIMIT 201 OFFSET ?`, r.URL.Query().Get("source"), r.URL.Query().Get("source"), r.URL.Query().Get("q"), r.URL.Query().Get("q"), offset(r))
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
func (a *App) readEvents(session string, limit, off int) ([]Event, error) {
	rows, err := a.DB.Query("SELECT id,session,parent,source,signal,time,name,trace,span,parent_span,raw FROM events WHERE session=? ORDER BY time,id LIMIT ? OFFSET ?", session, limit, off)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Session, &e.Parent, &e.Source, &e.Signal, &e.Time, &e.Name, &e.Trace, &e.Span, &e.ParentSpan, &e.Raw); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
	if err = a.DB.QueryRow("SELECT raw FROM events WHERE session=? ORDER BY (instr(lower(raw), 'prompt')>0) DESC,time,id LIMIT 1", id).Scan(&preview); err != nil {
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
