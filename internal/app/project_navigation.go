package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type ProjectSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Count int    `json:"count"`
	End   string `json:"end"`
}

func updateProjectTotals(ctx context.Context, tx *sql.Tx, changes map[folderKey]int) error {
	for key, delta := range changes {
		if delta != 0 {
			_, err := tx.ExecContext(ctx, `INSERT INTO project_totals VALUES(?,?,?,?, '') ON CONFLICT(project,source,diagnostic) DO UPDATE SET count=count+excluded.count`, key.Path, key.Source, key.Diagnostic, delta)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM project_totals WHERE project=? AND source=? AND diagnostic=? AND count=0", key.Path, key.Source, key.Diagnostic); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE project_totals SET end=(SELECT end FROM folder_members WHERE project=? AND source=? AND diagnostic=? ORDER BY end DESC,session LIMIT 1) WHERE project=? AND source=? AND diagnostic=?`, key.Path, key.Source, key.Diagnostic, key.Path, key.Source, key.Diagnostic)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) projects(w http.ResponseWriter, r *http.Request) {
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	filter, args := " FROM project_totals WHERE 1=1", []any{}
	if source := r.URL.Query().Get("source"); source != "" {
		filter += " AND source=?"
		args = append(args, source)
	}
	if r.URL.Query().Get("diagnostics") != "1" {
		filter += " AND diagnostic=0"
	}
	if query := strings.TrimSpace(r.URL.Query().Get("q")); query != "" {
		filter += " AND instr(lower(project),lower(?))>0"
		args = append(args, query)
	}
	var total, sessions int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(DISTINCT project),COALESCE(SUM(count),0)"+filter, args...).Scan(&total, &sessions); err != nil {
		databaseError(w, r, err)
		return
	}
	offset := pageOffset(offset(r), total, 200)
	params := append(append([]any{}, args...), offset)
	rows, err := tx.QueryContext(r.Context(), "SELECT project,SUM(count),MAX(end)"+filter+" GROUP BY project ORDER BY (project=''),lower(project),project LIMIT 200 OFFSET ?", params...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	projects := []ProjectSummary{}
	for rows.Next() {
		var project ProjectSummary
		if err = rows.Scan(&project.ID, &project.Count, &project.End); err != nil {
			break
		}
		switch {
		case strings.HasPrefix(project.ID, "name:"):
			project.Name = strings.TrimPrefix(project.ID, "name:")
		case strings.HasPrefix(project.ID, "path:"):
			project.Path = strings.TrimPrefix(project.ID, "path:")
			parts := strings.FieldsFunc(strings.TrimRight(project.Path, "/\\"), func(r rune) bool { return r == '/' || r == '\\' })
			project.Name = project.Path
			if len(parts) > 0 {
				project.Name = parts[len(parts)-1]
			}
		default:
			project.Name = "Unassigned"
		}
		projects = append(projects, project)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, map[string]any{"projects": projects, "total": total, "sessions": sessions, "offset": offset})
}
