package app

import (
	"context"
	"net/url"
	"strings"
)

// Both SQL and summary-cache eligibility use the same parsed filter. Empty
// project/folder values are meaningful (unassigned), unlike an absent filter.
type sessionFilter struct {
	Source, Search, Skill, Plugin string
	IDKey, IDValue                string
	Diagnostics                   bool
	Project, Folder               *string
}

func parseSessionFilter(q url.Values) sessionFilter {
	f := sessionFilter{Source: q.Get("source"), Search: strings.TrimSpace(q.Get("q")), Skill: q.Get("skill"), Plugin: q.Get("plugin"), Diagnostics: q.Get("diagnostics") == "1", IDKey: q.Get("id_key"), IDValue: q.Get("id_value")}
	if q.Has("project") {
		v := q.Get("project")
		f.Project = &v
	}
	if q.Has("folder") {
		v := q.Get("folder")
		f.Folder = &v
	}
	return f
}

// The folder cache has dimensions for source, diagnostics and folder only.
func (f sessionFilter) usesFolderCache() bool {
	return f.Search == "" && f.Skill == "" && f.Project == nil && f.IDKey == "" && f.IDValue == ""
}

func (f sessionFilter) sql(ctx context.Context) (string, []any, error) {
	query := ` FROM session_catalog s WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=s.session)`
	args := []any{}
	idQuery, idArgs, err := identifierFilter(f.IDKey, f.IDValue)
	if err != nil {
		return "", nil, err
	}
	query += idQuery
	args = append(args, idArgs...)
	if skill := f.Skill; skill != "" {
		query += ` AND EXISTS(SELECT 1 FROM skill_events k WHERE k.session=s.session AND k.name=? AND k.plugin=?)`
		args = append(args, skill, f.Plugin)
	}
	if !f.Diagnostics {
		query += ` AND s.internal=0 AND NOT (s.source='codex' AND (s.session GLOB 'trace:codex:*' OR s.session='unassigned:codex'))`
	}
	if source := f.Source; source != "" {
		query += " AND source=?"
		args = append(args, source)
	}
	if search := f.Search; search != "" {
		match, err := searchMatch(ctx, search)
		if err != nil {
			return "", nil, err
		}
		if match == "" {
			query += " AND 0"
		} else {
			query += ` AND s.session IN (SELECT d.session FROM event_search CROSS JOIN search_documents d ON d.rowid=event_search.rowid WHERE event_search MATCH ? GROUP BY d.session)`
			args = append(args, match)
		}
	}
	if f.Project != nil {
		query += " AND s.project_key=?"
		args = append(args, *f.Project)
	}
	if f.Folder != nil {
		query += " AND " + directoryColumn + "=?"
		args = append(args, *f.Folder)
	}
	return query, args, nil
}
