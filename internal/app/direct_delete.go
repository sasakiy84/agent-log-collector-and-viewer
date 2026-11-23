package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const deletionPageSize = 200

const knownSessionTime = "1970-01-01T00:00:00.000000000Z"

type deletionSession struct {
	ID           string `json:"id"`
	Revision     string `json:"revision"`
	Title        string `json:"title"`
	Folder       string `json:"folder"`
	Source       string `json:"source"`
	LastActivity string `json:"last_activity"`
	Records      int64  `json:"records"`
	RawBytes     int64  `json:"raw_bytes"`
}

func (a *App) directDeleteList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	order := "t.raw_bytes DESC,s.session"
	switch q.Get("sort") {
	case "", "largest":
	case "oldest":
		order = "s.end,s.session"
	case "newest":
		order = "s.end DESC,s.session"
	default:
		http.Error(w, "Invalid session sort order", http.StatusBadRequest)
		return
	}
	condition := ""
	var args []any
	if value := q.Get("days"); value != "" {
		days, err := strconv.Atoi(value)
		if err != nil || days < 0 || days > 36500 {
			http.Error(w, "Enter an age from 0 to 36500 days", http.StatusBadRequest)
			return
		}
		condition = " WHERE s.end>? AND s.end<=?"
		args = append(args, knownSessionTime, timestamp(time.Now().UTC().AddDate(0, 0, -days)))
	}
	tx, err := a.readers.BeginTx(ctx, nil)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	latest, err := scanArchive(tx.QueryRowContext(ctx, "SELECT "+archiveColumns+" FROM archive_jobs WHERE direct_delete=1 ORDER BY created_at DESC,id DESC LIMIT 1"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		databaseError(w, r, err)
		return
	}
	var job *ArchiveJob
	if err == nil {
		job = &latest
	}
	sessions := []deletionSession{}
	var total int
	var totalBytes int64
	if job == nil || job.Status != "deleting" && job.Status != "compacting" {
		const candidates = " FROM sessions s JOIN record_totals t ON t.session=s.session"
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(t.raw_bytes),0)"+candidates+condition, args...).Scan(&total, &totalBytes); err != nil {
			databaseError(w, r, err)
			return
		}
		offset = pageOffset(max(0, offset), total, deletionPageSize)
		rows, err := tx.QueryContext(ctx, `SELECT s.session,CAST(COALESCE(v.revision,0) AS TEXT),COALESCE(json_extract(s.preview,'$.blocks[0].text'),''),
			COALESCE(json_extract(s.preview,'$.cwd'),''),s.source,CASE WHEN s.end>? THEN s.end ELSE '' END,t.records,t.raw_bytes`+
			candidates+" LEFT JOIN session_versions v ON v.session=s.session"+condition+" ORDER BY "+order+" LIMIT ? OFFSET ?",
			append(append([]any{knownSessionTime}, args...), deletionPageSize, offset)...)
		if err != nil {
			databaseError(w, r, err)
			return
		}
		for rows.Next() {
			var s deletionSession
			if err = rows.Scan(&s.ID, &s.Revision, &s.Title, &s.Folder, &s.Source, &s.LastActivity, &s.Records, &s.RawBytes); err != nil {
				break
			}
			sessions = append(sessions, s)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			databaseError(w, r, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, map[string]any{"sessions": sessions, "total_sessions": total, "total_bytes": totalBytes, "offset": offset,
		"job": job, "available_bytes": a.databaseAvailableBytes()})
}

type deletionSelection struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

var errDeletionStale = errors.New("selected sessions changed; refresh the list and select them again")

func (a *App) directDeleteCreate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Sessions     []deletionSelection `json:"sessions"`
		Confirmation bool                `json:"confirmation"`
	}
	if !archiveJSON(w, r, &input) {
		return
	}
	if !input.Confirmation || len(input.Sessions) == 0 || len(input.Sessions) > 2000 {
		http.Error(w, "Confirm a selection of 1 to 2000 sessions", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(input.Sessions))
	for _, session := range input.Sessions {
		revision, err := strconv.ParseInt(session.Revision, 10, 64)
		if session.ID == "" || seen[session.ID] || err != nil || revision < 0 {
			http.Error(w, "Invalid deletion selection", http.StatusBadRequest)
			return
		}
		seen[session.ID] = true
	}
	job, err := a.createDirectDelete(r.Context(), input.Sessions)
	if errors.Is(err, errDeletionStale) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		archiveError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	write(w, job)
}

func (a *App) createDirectDelete(ctx context.Context, selection []deletionSelection) (ArchiveJob, error) {
	if a.dbPath == ":memory:" {
		return ArchiveJob{}, fmt.Errorf("session deletion requires a persistent database")
	}
	id, err := newMaintenanceJobID()
	if err != nil {
		return ArchiveJob{}, err
	}
	selected, err := json.Marshal(selection)
	if err != nil {
		return ArchiveJob{}, err
	}
	now := timestamp(time.Now())
	err = inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if err := availableMaintenanceSlot(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO archive_jobs(id,status,created_at,cutoff,confirmed_at,database_bytes_before,direct_delete)
			VALUES(?,'deleting',?,?,?,?,1)`, id, now, now, now, a.diskBytes()); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO archive_sessions(archive_id,session,source,revision,records,raw_bytes,last_activity)
			SELECT ?,s.session,s.source,COALESCE(v.revision,0),t.records,t.raw_bytes,s.end
			FROM json_each(?) wanted JOIN sessions s ON s.session=wanted.value->>'id'
			JOIN record_totals t ON t.session=s.session LEFT JOIN session_versions v ON v.session=s.session
			WHERE COALESCE(v.revision,0)=CAST(wanted.value->>'revision' AS INTEGER)`, id, string(selected))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != int64(len(selection)) {
			return errDeletionStale
		}
		pending, err := a.archiveHasPending(ctx, tx, id)
		if err != nil {
			return err
		}
		if pending {
			return errDeletionStale
		}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_jobs SET session_count=?,
			record_count=(SELECT SUM(records) FROM archive_sessions WHERE archive_id=?),
			raw_bytes=(SELECT SUM(raw_bytes) FROM archive_sessions WHERE archive_id=?) WHERE id=?`, count, id, id, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO archive_locks SELECT session,archive_id FROM archive_sessions WHERE archive_id=?", id); err != nil {
			return err
		}
		return refreshFolders(ctx, tx)
	})
	if err != nil {
		return ArchiveJob{}, err
	}
	a.notifyArchive()
	a.wakeArchive()
	return a.archiveJob(ctx, id)
}
