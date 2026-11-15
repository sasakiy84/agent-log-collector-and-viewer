package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed static/*
var assets embed.FS

type App struct {
	DB                 *sql.DB
	readers            *sql.DB
	token              string
	dbPath, archiveDir string
	ctx                context.Context
	cancel             context.CancelFunc
	workers            sync.WaitGroup
	archiveWake        chan struct{}
	archiveMu          sync.Mutex
	archiveChange      chan struct{}
	taskMu             sync.Mutex
	taskID             string
	taskCancel         context.CancelFunc
	compacting         atomic.Bool
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
func databaseError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, "Database error", http.StatusInternalServerError)
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
	mux.HandleFunc("GET /api/archive", a.archiveList)
	mux.HandleFunc("POST /api/archives", a.archiveCreate)
	mux.HandleFunc("GET /api/archives/{id}", a.archiveStatus)
	mux.HandleFunc("GET /api/archives/{id}/events", a.archiveEvents)
	mux.HandleFunc("GET /api/archives/{id}/download", a.archiveDownload)
	mux.HandleFunc("POST /api/archives/{id}/delete", a.archiveDelete)
	mux.HandleFunc("POST /api/archives/{id}/cancel", a.archiveCancel)
	mux.HandleFunc("POST /api/archives/{id}/compact", a.archiveCompact)
	mux.HandleFunc("GET /archive", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/sessions", a.sessions)
	mux.HandleFunc("GET /api/folders", a.folders)
	mux.HandleFunc("GET /api/projects", a.projects)
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("GET /api/event", a.event)
	mux.HandleFunc("GET /api/subagent", a.subagent)
	mux.HandleFunc("GET /api/skills", a.skills)
	mux.HandleFunc("GET /skills", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/export", a.export)
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		var n, s int
		if err := a.readers.QueryRow("SELECT COUNT(*),COUNT(DISTINCT session) FROM events").Scan(&n, &s); err != nil {
			databaseError(w, r, err)
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
