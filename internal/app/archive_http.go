package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"
)

func archiveJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "Content-Type must be application/json", 415)
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(v); err != nil {
		http.Error(w, "Invalid request", 400)
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid request", 400)
		return false
	}
	return true
}
func archiveError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errArchiveConflict) || errors.Is(err, errArchiveStale) {
		http.Error(w, err.Error(), 409)
	} else if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else {
		databaseError(w, r, err)
	}
}
func (a *App) archiveList(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	cutoff := timestamp(archiveCutoff(time.Now()))
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	job, e := scanArchive(tx.QueryRowContext(r.Context(), "SELECT "+archiveColumns+" FROM archives ORDER BY created_at DESC LIMIT 1"))
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		databaseError(w, r, e)
		return
	}
	var current *ArchiveJob
	if e == nil {
		current = &job
	}
	query := inactiveSessions
	args := []any{cutoff}
	if current != nil && activeArchive(job.Status) {
		query = "SELECT session,source,last_activity,records,raw_bytes FROM archive_sessions WHERE archive_id=?"
		args = []any{job.ID}
		cutoff = job.Cutoff
	}
	var total, records, rawBytes int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*),COALESCE(SUM(records),0),COALESCE(SUM(raw_bytes),0) FROM ("+query+")", args...).Scan(&total, &records, &rawBytes); err != nil {
		databaseError(w, r, err)
		return
	}
	args = append(args, offset)
	rows, err := tx.QueryContext(r.Context(), "SELECT * FROM ("+query+") ORDER BY last_activity,session LIMIT 201 OFFSET ?", args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	sessions := []ArchiveSession{}
	for rows.Next() {
		var s ArchiveSession
		if err = rows.Scan(&s.ID, &s.Source, &s.LastActivity, &s.Records, &s.RawBytes); err != nil {
			break
		}
		sessions = append(sessions, s)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		databaseError(w, r, errors.Join(err, rowErr))
		return
	}
	write(w, map[string]any{"cutoff": cutoff, "total_sessions": total, "total_records": records, "total_bytes": rawBytes, "sessions": sessions, "job": current})
}
func (a *App) archiveCreate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		All      bool     `json:"all"`
		Sessions []string `json:"sessions"`
		Cutoff   string   `json:"cutoff"`
	}
	if !archiveJSON(w, r, &input) {
		return
	}
	cutoff, err := time.Parse(time.RFC3339Nano, input.Cutoff)
	if err != nil || cutoff.After(archiveCutoff(time.Now())) || len(input.Sessions) > 2000 || (!input.All && len(input.Sessions) == 0) || (input.All && len(input.Sessions) != 0) {
		http.Error(w, "Invalid archive selection", 400)
		return
	}
	seen := map[string]bool{}
	for _, id := range input.Sessions {
		if id == "" || seen[id] {
			http.Error(w, "Invalid archive selection", 400)
			return
		}
		seen[id] = true
	}
	job, err := a.createArchive(r.Context(), timestamp(cutoff), input.All, input.Sessions)
	if err != nil {
		archiveError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	write(w, job)
}
func (a *App) archiveStatus(w http.ResponseWriter, r *http.Request) {
	job, err := a.archiveJob(r.Context(), r.PathValue("id"))
	if err != nil {
		archiveError(w, r, err)
		return
	}
	write(w, job)
}

type downloadWriter struct {
	http.ResponseWriter
	bytes  int64
	status int
}

func (w *downloadWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *downloadWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}
func (a *App) archiveDownload(w http.ResponseWriter, r *http.Request) {
	job, err := a.archiveJob(r.Context(), r.PathValue("id"))
	if err != nil {
		archiveError(w, r, err)
		return
	}
	if job.Status != "ready" {
		http.Error(w, "Archive is not ready", 409)
		return
	}
	file, err := os.Open(a.archivePath(job.ID, false))
	if err != nil {
		http.Error(w, "Archive file is unavailable; cancel and create a new archive", 409)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != job.ArchiveBytes {
		http.Error(w, "Archive file is incomplete", 409)
		return
	}
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="agentlog-%s.zip"`, job.ID))
	w.Header().Set("ETag", `"`+job.SHA256+`"`)
	w.Header().Set("X-Accel-Buffering", "no")
	tracked := &downloadWriter{ResponseWriter: w}
	http.ServeContent(tracked, r, "archive.zip", info.ModTime(), file)
	if r.Method == "GET" && tracked.bytes > 0 && (tracked.status == http.StatusOK || tracked.status == http.StatusPartialContent) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err = a.DB.ExecContext(ctx, "UPDATE archives SET download_at=? WHERE id=? AND status='ready'", timestamp(time.Now()), job.ID); err != nil {
			log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		}
	}
}
func (a *App) archiveDelete(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if !archiveJSON(w, r, &input) {
		return
	}
	if input.Confirmation != archiveConfirmation {
		http.Error(w, "Type DOWNLOADED to confirm your download", 400)
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	job, err := scanArchive(tx.QueryRowContext(r.Context(), "SELECT "+archiveColumns+" FROM archives WHERE id=?", r.PathValue("id")))
	if err != nil {
		archiveError(w, r, err)
		return
	}
	if job.Status != "ready" || job.DownloadAt == "" {
		http.Error(w, "Download the ready archive before confirming deletion", 409)
		return
	}
	valid, err := snapshotsCurrent(r.Context(), tx, job.ID)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if !valid {
		archiveError(w, r, errArchiveStale)
		return
	}
	info, err := os.Stat(a.archivePath(job.ID, false))
	if err != nil || info.Size() != job.ArchiveBytes {
		http.Error(w, "Archive is unavailable; cancel and create a new archive", 409)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO archive_locks SELECT session,archive_id FROM archive_sessions WHERE archive_id=?", job.ID); err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE archives SET status='deleting',confirmed_at=?,database_bytes_before=? WHERE id=?", timestamp(time.Now()), a.diskBytes(), job.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	a.wakeArchive()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	write(w, map[string]bool{"accepted": true})
}
func (a *App) archiveCancel(w http.ResponseWriter, r *http.Request) {
	if !archiveJSON(w, r, &struct{}{}) {
		return
	}
	id := r.PathValue("id")
	result, err := a.DB.ExecContext(r.Context(), "UPDATE archives SET status='canceled' WHERE id=? AND status IN ('preparing','ready')", id)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "Archive cannot be canceled", 409)
		return
	}
	a.taskMu.Lock()
	if a.taskID == id && a.taskCancel != nil {
		a.taskCancel()
	}
	a.taskMu.Unlock()
	// Use the canonical database ID rather than an untrusted path segment.
	job, err := a.archiveJob(r.Context(), id)
	if err == nil {
		os.Remove(a.archivePath(job.ID, false))
	}
	write(w, map[string]bool{"ok": true})
}
func (a *App) archiveCompact(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Rebuild bool `json:"rebuild"`
	}
	if !archiveJSON(w, r, &input) {
		return
	}
	job, err := a.archiveJob(r.Context(), r.PathValue("id"))
	if err != nil {
		archiveError(w, r, err)
		return
	}
	if job.Status != "deleted" || job.CompactionError == "" || job.RebuildRequired && !input.Rebuild {
		http.Error(w, "A database rebuild requires an explicit rebuild request", 409)
		return
	}
	result, err := a.DB.ExecContext(r.Context(), "UPDATE archives SET status='compacting',rebuild_requested=? WHERE id=? AND status='deleted' AND NOT EXISTS(SELECT 1 FROM archives WHERE status IN ('preparing','ready','deleting','compacting'))", input.Rebuild, job.ID)
	if err != nil {
		archiveError(w, r, err)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "Archive state changed", 409)
		return
	}
	a.wakeArchive()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	write(w, map[string]bool{"accepted": true})
}
