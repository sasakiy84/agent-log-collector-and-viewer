package app

import (
	"database/sql"
	"errors"
	"net/http"
)

type ArchiveFolder struct {
	Path         string `json:"path"`
	Sessions     int64  `json:"sessions"`
	Records      int64  `json:"records"`
	RawBytes     int64  `json:"raw_bytes"`
	LastActivity string `json:"last_activity"`
}

func (a *App) archiveFolderList(w http.ResponseWriter, r *http.Request, tx *sql.Tx, query string, args []any, cutoff string, job *ArchiveJob, off int) {
	grouped := "SELECT folder,COUNT(*) AS sessions,SUM(records) AS records,SUM(raw_bytes) AS raw_bytes,MAX(last_activity) AS last_activity FROM (" + query + ") GROUP BY folder"
	var total int
	var sessions, records, rawBytes int64
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*),COALESCE(SUM(sessions),0),COALESCE(SUM(records),0),COALESCE(SUM(raw_bytes),0) FROM ("+grouped+")", args...).Scan(&total, &sessions, &records, &rawBytes); err != nil {
		databaseError(w, r, err)
		return
	}
	off = pageOffset(off, total, 200)
	params := append(append([]any{}, args...), off)
	rows, err := tx.QueryContext(r.Context(), grouped+" ORDER BY last_activity,folder LIMIT 200 OFFSET ?", params...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	folders := []ArchiveFolder{}
	for rows.Next() {
		var folder ArchiveFolder
		if err = rows.Scan(&folder.Path, &folder.Sessions, &folder.Records, &folder.RawBytes, &folder.LastActivity); err != nil {
			break
		}
		folders = append(folders, folder)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		databaseError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, map[string]any{"folders": folders, "total_folders": total, "total_sessions": sessions, "total_records": records, "total_bytes": rawBytes, "offset": off, "cutoff": cutoff, "job": job, "available_bytes": a.archiveAvailableBytes()})
}
