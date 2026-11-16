package app

import (
	"net/http"
)

// Analysis is an explicit request, never part of initial list loading. It uses
// the same event predicate as search and reads cached metadata, not raw logs.
func (a *App) searchSummary(w http.ResponseWriter, r *http.Request) {
	filter := parseSessionFilter(r.URL.Query())
	cards, err := filter.matchingCards(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	membership := filter
	membership.Search = ""
	membership.Exclude = ""
	membership.Skill = ""
	membership.Facets.From = ""
	membership.Facets.To = ""
	membership.Facets.Origins = nil
	membership.Facets.Kinds = nil
	metadata, err := membership.selection(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if filter.Search == "" {
		// With no FTS posting list, project/source catalog rows drive indexed
		// session/origin/time probes instead of a scan of all cached card payloads.
		cards.from = " FROM session_catalog s CROSS JOIN event_cards c ON c.session=s.session"
	} else {
		cards.from += " JOIN session_catalog s ON s.session=c.session"
	}
	cards.where += metadata.where + " AND length(s.parent)=0"
	cards.args = append(cards.args, metadata.args...)
	if filter.Skill != "" {
		evidence, more := filter.Facets.evidenceSQL("k")
		cards.where += ` AND EXISTS(SELECT 1 FROM skill_events k WHERE k.session=s.session AND k.name=? AND k.plugin=?` + evidence + `)`
		cards.args = append(cards.args, filter.Skill, filter.Plugin)
		cards.args = append(cards.args, more...)
	}
	group := r.URL.Query().Get("group")
	column := ""
	var columnArgs []any
	switch group {
	case "", "day":
		group = "day"
		column = "substr(c.time,1,10)"
		if filter.Skill != "" {
			evidence, more := filter.Facets.evidenceSQL("k")
			column = `substr((SELECT MIN(k.time) FROM skill_events k WHERE k.event_id=c.id AND k.name=? AND k.plugin=?` + evidence + `),1,10)`
			columnArgs = append([]any{filter.Skill, filter.Plugin}, more...)
		}
	case "project":
		column = "s.project_key"
	case "source":
		column = "s.source"
	case "kind":
		column = "c.kind"
	default:
		http.Error(w, "Invalid analysis grouping", 400)
		return
	}
	order := "group_key DESC"
	if group != "day" {
		order = "COUNT(*) DESC,group_key"
	}
	rows, err := a.readers.QueryContext(r.Context(), "SELECT "+column+" AS group_key,COUNT(*),COUNT(DISTINCT c.session),SUM(c.is_error)"+cards.sql()+" GROUP BY group_key ORDER BY "+order+" LIMIT 101", append(columnArgs, cards.args...)...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer rows.Close()
	type bucket struct {
		Key      string `json:"key"`
		Records  int64  `json:"records"`
		Sessions int64  `json:"sessions"`
		Errors   int64  `json:"errors"`
	}
	groups := []bucket{}
	for rows.Next() {
		var b bucket
		if err = rows.Scan(&b.Key, &b.Records, &b.Sessions, &b.Errors); err != nil {
			break
		}
		groups = append(groups, b)
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	limited := len(groups) > 100
	if limited {
		groups = groups[:100]
	}
	write(w, map[string]any{"groups": groups, "group": group, "limited": limited, "timezone": "UTC"})
}
