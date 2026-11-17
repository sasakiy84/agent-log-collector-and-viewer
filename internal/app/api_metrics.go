package app

import (
	"database/sql"
	_ "embed"
	"net/http"
	"time"
)

//go:embed api/README.md
var apiReference string

func (a *App) apiReadme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(apiReference))
}

type serverMetrics struct {
	Version       string `json:"version"`
	StartedAt     string `json:"started_at"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	CountsReady   bool   `json:"counts_ready"`
	Events        *int64 `json:"events"`
	Sessions      *int64 `json:"sessions"`
	LogicalEvents *int64 `json:"logical_events"`
	RawBytes      *int64 `json:"raw_bytes"`
	serviceStatus
}

// Raw counters are already maintained for archives in the events transaction.
// Metrics read those compact rows, not full event bodies or a raw-history COUNT.
func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	state, err := a.statusSnapshot(r.Context())
	if err != nil {
		databaseError(w, r, err)
		return
	}
	out := serverMetrics{Version: a.Version, StartedAt: a.startedAt.UTC().Format(time.RFC3339Nano), UptimeSeconds: int64(time.Since(a.startedAt).Seconds()), serviceStatus: state}
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var events, sessions, bytes int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(records),0),COUNT(*),COALESCE(SUM(raw_bytes),0) FROM record_totals").Scan(&events, &sessions, &bytes); err != nil {
		databaseError(w, r, err)
		return
	}
	out.Events, out.Sessions, out.RawBytes = &events, &sessions, &bytes
	out.CountsReady = true
	if state.State == "ready" {
		var logical int64
		if err = tx.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(count),0) FROM timeline_counts").Scan(&logical); err != nil {
			databaseError(w, r, err)
			return
		}
		out.LogicalEvents = &logical
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, out)
}
