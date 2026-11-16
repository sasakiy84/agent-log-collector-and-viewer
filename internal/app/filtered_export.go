package app

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Exports include all matching stored sessions by default, including children,
// internal conversations and archive-locked data that has not been deleted.
func exportSessionFilter(q url.Values) sessionFilter {
	f := parseSessionFilter(q)
	f.IncludeLocked = q.Get("include_locked") != "0"
	if !q.Has("diagnostics") {
		f.Diagnostics = true
	}
	return f
}

func (a *App) filteredExport(w http.ResponseWriter, r *http.Request) {
	filter := exportSessionFilter(r.URL.Query())
	query, args, err := filter.sql(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var sessions, expected int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*),COALESCE(SUM(s.import_count+s.otel_count),0)"+query, args...).Scan(&sessions, &expected); err != nil {
		databaseError(w, r, err)
		return
	}
	if sessions == 0 {
		http.Error(w, "No sessions match these filters", http.StatusNotFound)
		return
	}
	// Count, membership and event rows share a WAL snapshot. A concurrent
	// arrival/deletion cannot produce a manifest describing a different dataset.
	rows, err := tx.QueryContext(r.Context(), "SELECT s.session"+query+" ORDER BY s.session", args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer rows.Close()
	prepareDownload(w, "application/zip", "agentlog-export.zip")
	archive := zip.NewWriter(w)
	err = writeFilteredExport(r.Context(), tx, rows, archive, filter, r.URL.Query(), sessions, expected)
	if err == nil {
		err = archive.Close()
	}
	if err != nil {
		// Never finalize a partial archive as a successful download, or append
		// an HTTP error message after ZIP bytes have already been sent.
		abortDownload(err)
	}
}

func writeFilteredExport(ctx context.Context, tx *sql.Tx, sessions *sql.Rows, archive *zip.Writer, filter sessionFilter, q url.Values, count, expected int64) error {
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	filters := map[string]any{}
	for _, key := range []string{"project", "folder", "q", "id_key", "id_value", "source", "skill", "plugin", "from", "to", "origin", "event_kind", "text_mode", "exclude", "include_children", "include_locked"} {
		if q.Has(key) {
			if len(q[key]) > 1 {
				filters[key] = q[key]
			} else {
				filters[key] = q.Get(key)
			}
		}
	}
	if len(filter.Facets.Projects) > 1 {
		filters["project"] = filter.Facets.Projects
	} else if filter.Project != nil {
		filters["project"] = *filter.Project
	}
	info := struct {
		Format    string         `json:"format"`
		Version   int            `json:"version"`
		CreatedAt string         `json:"created_at"`
		Filters   map[string]any `json:"filters"`
		Internal  bool           `json:"include_internal"`
		Sessions  int64          `json:"sessions"`
		Records   int64          `json:"records"`
	}{"agentlog.session-export", 1, time.Now().UTC().Format(time.RFC3339), filters, filter.Diagnostics, count, expected}
	if err = json.NewEncoder(manifest).Encode(info); err != nil {
		return err
	}
	destination, err := archive.Create("events.jsonl")
	if err != nil {
		return err
	}
	if err = archive.Flush(); err != nil {
		return err
	}
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	var exported int64
	for sessions.Next() {
		var session string
		if err = sessions.Scan(&session); err != nil {
			return err
		}
		n, err := writeExportSession(ctx, tx, encoder, session)
		if err != nil {
			return err
		}
		exported += n
	}
	if err = sessions.Err(); err != nil {
		return err
	}
	if exported != expected {
		return errors.New("export record count differs from session summaries")
	}
	return ctx.Err()
}

func writeExportSession(ctx context.Context, tx *sql.Tx, encoder *json.Encoder, session string) (int64, error) {
	// events_session supplies this order without sorting/buffering raw logs.
	rows, err := tx.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE session=? ORDER BY origin,time", session)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return count, err
		}
		if err = encoder.Encode(event); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
