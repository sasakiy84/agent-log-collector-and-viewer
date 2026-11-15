package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
)

const directoryColumn = "COALESCE(json_extract(preview,'$.cwd'),'')"

func directoryPath(value any) string {
	folder, ok := value.(string)
	if !ok || len(folder) > 32*1024 {
		return ""
	}
	return folder
}

type FolderSummary struct {
	Path    string   `json:"path"`
	Count   int      `json:"count"`
	End     string   `json:"end"`
	Sources []string `json:"sources"`
}

func (a *App) folders(w http.ResponseWriter, r *http.Request) {
	filter, args, err := sessionFilter(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	query := "SELECT " + directoryColumn + " AS path,COUNT(*) AS count,MAX(end) AS end,GROUP_CONCAT(DISTINCT source) AS sources" + filter + " GROUP BY " + directoryColumn
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM ("+query+")", args...).Scan(&total); err != nil {
		databaseError(w, r, err)
		return
	}
	off := pageOffset(offset(r), total, 200)
	args = append(args, off)
	rows, err := tx.QueryContext(r.Context(), query+" ORDER BY end DESC,path LIMIT 200 OFFSET ?", args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	out := []FolderSummary{}
	for rows.Next() {
		var folder FolderSummary
		var sources string
		if err = rows.Scan(&folder.Path, &folder.Count, &folder.End, &sources); err != nil {
			break
		}
		folder.Sources = strings.Split(sources, ",")
		sort.Strings(folder.Sources)
		out = append(out, folder)
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
	write(w, map[string]any{"folders": out, "total": total, "offset": off})
}

const directoryCandidates = `SELECT id,json_extract(detail,'$.cwd') FROM event_cards
 WHERE session=? AND json_type(detail,'$.cwd')='text' AND json_extract(detail,'$.cwd')<>''
 ORDER BY is_user DESC,time,sequence,id LIMIT 1`

// The first user event with a directory wins; other known events supply a
// fallback. A partial index also makes sessions with no directory a cheap read.
func sessionDirectory(ctx context.Context, tx *sql.Tx, session string) (string, error) {
	for {
		var id, folder string
		err := tx.QueryRowContext(ctx, directoryCandidates, session).Scan(&id, &folder)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		stored := folder
		// Old presentations clipped metadata at 512 bytes. Recover only the
		// selected record, without re-reading the session's original history.
		if len(folder) >= 512 && len(folder) <= 515 && strings.HasSuffix(folder, "…") {
			e, eErr := scanEvent(tx.QueryRowContext(ctx, "SELECT "+qualifiedColumns("e")+" FROM events e WHERE id=?", id))
			if eErr != nil {
				return "", eErr
			}
			doc, eErr := decodeDocument(e.Raw)
			if eErr != nil {
				return "", eErr
			}
			folder = directoryPath(describe(e, doc).Cwd)
		}
		clean := directoryPath(folder)
		if clean != stored {
			if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET detail=json_set(detail,'$.cwd',?) WHERE id=?", clean, id); err != nil {
				return "", err
			}
		}
		if clean != "" {
			return clean, nil
		}
		// Discard an invalid legacy candidate and try the next known directory.
	}
}

// Populate folder metadata once for existing indexed sessions. The upgrade
// reads narrow summaries and one indexed directory candidate per session.
func (a *App) initDirectoryIndex() error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version >= 1 {
		return err
	}
	rows, err := tx.Query("SELECT session FROM session_catalog")
	if err != nil {
		return err
	}
	sessions := []string{}
	for rows.Next() {
		var session string
		if err = rows.Scan(&session); err != nil {
			break
		}
		sessions = append(sessions, session)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	for _, session := range sessions {
		folder, e := sessionDirectory(context.Background(), tx, session)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE session_catalog SET preview=json_set(preview,'$.cwd',?) WHERE session=?", folder, session); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=1"); err != nil {
		return err
	}
	return tx.Commit()
}
