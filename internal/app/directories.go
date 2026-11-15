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
	Path    string       `json:"path"`
	Count   int          `json:"count"`
	End     string       `json:"end"`
	Sources []string     `json:"sources"`
	Usage   UsageSummary `json:"usage"`
}

func (a *App) folders(w http.ResponseWriter, r *http.Request) {
	selection := parseSessionFilter(r.URL.Query())
	filter, args, err := selection.sql(r.Context())
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
	if selection.usesFolderCache() {
		var pending bool
		if err = tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM folder_dirty)").Scan(&pending); err != nil {
			databaseError(w, r, err)
			return
		}
		// External SQL edits can leave a pending projection; preserve correct
		// results until the next write/startup drains it. Normal writes publish
		// folder summaries in the same transaction as the session update.
		if !pending {
			folders, total, offset, e := cachedFolders(r.Context(), tx, selection.Source, selection.Diagnostics, selection.Folder, offset(r))
			if e == nil {
				e = tx.Commit()
			}
			if e != nil {
				databaseError(w, r, e)
				return
			}
			write(w, map[string]any{"folders": folders, "total": total, "offset": offset})
			return
		}
	}
	var total int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM ("+query+")", args...).Scan(&total); err != nil {
		databaseError(w, r, err)
		return
	}
	off := pageOffset(offset(r), total, 200)
	usageArgs := append([]any(nil), args...)
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
	if err = folderUsage(r.Context(), tx, filter, usageArgs, out); err != nil {
		databaseError(w, r, err)
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

// Backfill folder summaries in bounded, resumable session-key batches.
func backfillDirectories(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT session FROM session_catalog WHERE session>? ORDER BY session LIMIT 128", cursor)
	if err != nil {
		return cursor, 0, err
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
		return cursor, 0, errors.Join(err, rowErr)
	}
	for _, session := range sessions {
		folder, err := sessionDirectory(ctx, tx, session)
		if err != nil {
			return cursor, 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE session_catalog SET preview=json_set(preview,'$.cwd',?) WHERE session=?", folder, session); err != nil {
			return cursor, 0, err
		}
		cursor = session
	}
	return cursor, len(sessions), nil
}
