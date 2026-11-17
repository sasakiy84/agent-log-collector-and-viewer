package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type ProjectSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	End   string `json:"end"`
}

// Normalize a directory-based selection to the name now used by every project.
// This also prevents old open/bookmarked URLs from recreating duplicate rows.
func canonicalProjectID(id string) string {
	directory, ok := strings.CutPrefix(id, "path:")
	if !ok {
		return id
	}
	if directory == "" {
		return ""
	}
	parts := strings.FieldsFunc(strings.TrimRight(directory, "/\\"), func(r rune) bool { return r == '/' || r == '\\' })
	name := directory
	if len(parts) > 0 {
		name = parts[len(parts)-1]
	}
	return "name:" + name
}

// Project identity is domain metadata, stored independently of the UI preview.
func sessionProject(d Detail) (string, string) {
	cwd, _ := d.Cwd.(string)
	directory := strings.ReplaceAll(strings.TrimRight(cwd, "/\\"), "\\", "/")
	if project, ok := d.Project.(string); ok && project != "" {
		return directory, "name:" + project
	}
	if cwd != "" {
		return directory, canonicalProjectID("path:" + cwd)
	}
	return directory, ""
}

type projectDelta struct{ Count, Roots int }

func updateProjectTotals(ctx context.Context, tx *sql.Tx, changes map[folderKey]projectDelta) error {
	for key, delta := range changes {
		if delta.Count != 0 || delta.Roots != 0 {
			_, err := tx.ExecContext(ctx, `INSERT INTO project_summaries(project,source,diagnostic,count,end,root_count,root_end) VALUES(?,?,?,?, '',?,'') ON CONFLICT(project,source,diagnostic) DO UPDATE SET count=count+excluded.count,root_count=root_count+excluded.root_count`, key.Path, key.Source, key.Diagnostic, delta.Count, delta.Roots)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM project_summaries WHERE project=? AND source=? AND diagnostic=? AND count=0", key.Path, key.Source, key.Diagnostic); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE project_summaries SET end=(SELECT end FROM session_membership WHERE project=? AND source=? AND diagnostic=? ORDER BY end DESC,session LIMIT 1),
 root_end=COALESCE((SELECT end FROM session_membership WHERE project=? AND source=? AND diagnostic=? AND child=0 ORDER BY end DESC,session LIMIT 1),'')
 WHERE project=? AND source=? AND diagnostic=?`, key.Path, key.Source, key.Diagnostic, key.Path, key.Source, key.Diagnostic, key.Path, key.Source, key.Diagnostic)
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
	countColumn, endColumn := "root_count", "root_end"
	if r.URL.Query().Get("include_children") == "1" {
		countColumn, endColumn = "count", "end"
	}
	filter, args := " FROM project_summaries WHERE "+countColumn+">0", []any{}
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

	projectColumn := "project"
	// Cached totals handle the common unfiltered navigation. Only explicit
	// search facets use the much smaller session catalog and indexed predicates.
	q := r.URL.Query()
	scopedQuery := url.Values{}
	for key, values := range q {
		scopedQuery[key] = append([]string{}, values...)
	}
	scopedQuery.Del("project")
	scopedQuery.Set("q", q.Get("session_q"))
	facets := parseSearchFacets(scopedQuery)
	if facets.Err != nil {
		http.Error(w, facets.Err.Error(), 400)
		return
	}
	if facets.hasEvents() || len(q["source"]) > 1 || q.Get("session_q") != "" || q.Get("exclude") != "" || q.Has("id_key") || q.Has("id_value") || q.Get("skill") != "" {
		f := parseSessionFilter(scopedQuery)
		f.IncludeLocked = q.Get("include_children") == "1"
		var e error
		filter, args, e = f.sql(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if !f.IncludeLocked {
			filter += " AND length(s.parent)=0"
		}
		if search := strings.TrimSpace(q.Get("q")); search != "" {
			filter += " AND instr(lower(s.project_key),lower(?))>0"
			args = append(args, search)
		}
		countColumn, endColumn, projectColumn = "1", "s.end", "s.project_key"
	}
	var total, sessions int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(DISTINCT "+projectColumn+"),COALESCE(SUM("+countColumn+"),0)"+filter, args...).Scan(&total, &sessions); err != nil {
		databaseError(w, r, err)
		return
	}
	offset := pageOffset(offset(r), total, 200)
	params := append(append([]any{}, args...), offset)
	rows, err := tx.QueryContext(r.Context(), "SELECT "+projectColumn+",SUM("+countColumn+"),MAX("+endColumn+")"+filter+" GROUP BY "+projectColumn+" ORDER BY MAX("+endColumn+") DESC,lower("+projectColumn+"),"+projectColumn+" LIMIT 200 OFFSET ?", params...)
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
		if project.ID == "" {
			project.Name = "Unassigned"
		} else {
			project.Name = strings.TrimPrefix(project.ID, "name:")
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
