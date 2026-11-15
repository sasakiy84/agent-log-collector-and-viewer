package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Both SQL and summary-cache eligibility use the same parsed filter. Empty
// project/folder values are meaningful (unassigned), unlike an absent filter.
type sessionFilter struct {
	Source, Search, Skill, Plugin string
	IDKey, IDValue                string
	TextMode, Exclude             string
	Facets                        searchFacets
	Diagnostics                   bool
	IncludeLocked                 bool
	RootsOnly                     bool
	Project, Folder               *string
}

func parseSessionFilter(q url.Values) sessionFilter {
	f := sessionFilter{RootsOnly: q.Get("include_children") == "0", Facets: parseSearchFacets(q), TextMode: q.Get("text_mode"), Exclude: strings.TrimSpace(q.Get("exclude")), Source: q.Get("source"), Search: strings.TrimSpace(q.Get("q")), Skill: q.Get("skill"), Plugin: q.Get("plugin"), Diagnostics: q.Get("diagnostics") == "1", IDKey: q.Get("id_key"), IDValue: q.Get("id_value")}
	if len(f.Facets.Sources) == 1 {
		f.Source = f.Facets.Sources[0]
	}
	if q.Has("project") {
		v := canonicalProjectID(q.Get("project"))
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
	return !f.RootsOnly && f.Facets.Err == nil && !f.Facets.hasEvents() && len(f.Facets.Projects) == 0 && len(f.Facets.Sources) <= 1 && f.Exclude == "" && !f.IncludeLocked && f.Search == "" && f.Skill == "" && f.Project == nil && f.IDKey == "" && f.IDValue == ""
}

func (f sessionFilter) sql(ctx context.Context) (string, []any, error) {
	query := ` FROM session_catalog s WHERE 1`
	if !f.IncludeLocked {
		query += ` AND NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=s.session)`
	}
	args := []any{}
	idQuery, idArgs, err := identifierFilter(f.IDKey, f.IDValue)
	if err != nil {
		return "", nil, err
	}
	query += idQuery
	args = append(args, idArgs...)
	if skill := f.Skill; skill != "" {
		evidence, evidenceArgs := f.Facets.evidenceSQL("k")
		query += ` AND EXISTS(SELECT 1 FROM skill_events k WHERE k.session=s.session AND k.name=? AND k.plugin=?` + evidence + `)`
		args = append(args, skill, f.Plugin)
		args = append(args, evidenceArgs...)
	}
	if f.RootsOnly {
		query += " AND length(s.parent)=0"
	}
	if !f.Diagnostics {
		query += ` AND s.internal=0 AND NOT (s.source IN('codex','claude','copilot') AND (s.session GLOB ('trace:'||s.source||':*') OR s.session='unassigned:'||s.source))`
	}

	if f.Facets.Err != nil {
		return "", nil, f.Facets.Err
	}
	if f.TextMode != "" && f.TextMode != "all" && f.TextMode != "any" {
		return "", nil, fmt.Errorf("Invalid text match mode")
	}
	scope, scopeArgs := f.Facets.sessionSQL()
	query += scope
	args = append(args, scopeArgs...)
	if len(f.Facets.Sources) == 0 && f.Source != "" {
		query += " AND s.source=?"
		args = append(args, f.Source)
	}
	if len(f.Facets.Projects) == 0 && f.Project != nil {
		query += " AND s.project_key=?"
		args = append(args, *f.Project)
	}
	if f.Search != "" || f.Exclude != "" || f.Facets.hasEvents() {
		cards, cardArgs, err := f.matchingCards(ctx)
		if err != nil {
			return "", nil, err
		}
		if f.Search != "" {
			query += " AND s.session IN(SELECT c.session" + cards + " GROUP BY c.session)"
		} else {
			query += " AND EXISTS(SELECT 1" + cards + " AND c.session=s.session)"
		}
		args = append(args, cardArgs...)
	}
	if f.Folder != nil {
		query += " AND " + directoryColumn + "=?"
		args = append(args, *f.Folder)
	}
	return query, args, nil
}
