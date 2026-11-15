package app

import (
	"net/http"
	"strings"
)

// Analysis is an explicit request, never part of initial list loading. It uses
// the same event predicate as search and reads cached metadata, not raw logs.
func (a *App) searchSummary(w http.ResponseWriter, r *http.Request) {
	filter := parseSessionFilter(r.URL.Query())
	cards, args, err := filter.matchingCards(r.Context())
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
	metadata, metadataArgs, err := membership.sql(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	split := strings.Index(cards, " WHERE 1")
	if filter.Search == "" {
		// With no FTS posting list, project/source catalog rows drive indexed
		// session/origin/time probes instead of a scan of all cached card payloads.
		cards = " FROM session_catalog s CROSS JOIN event_cards c ON c.session=s.session" + cards[split:]
	} else {
		cards = cards[:split] + " JOIN session_catalog s ON s.session=c.session" + cards[split:]
	}
	cards += strings.TrimPrefix(metadata, " FROM session_catalog s WHERE 1") + " AND length(s.parent)=0"
	args = append(args, metadataArgs...)
	if filter.Skill != "" {
		evidence, more := filter.Facets.evidenceSQL("k")
		cards += ` AND EXISTS(SELECT 1 FROM skill_events k WHERE k.session=s.session AND k.name=? AND k.plugin=?` + evidence + `)`
		args = append(args, filter.Skill, filter.Plugin)
		args = append(args, more...)
	}
	group := r.URL.Query().Get("group")
	column := ""
	switch group {
	case "", "day":
		group = "day"
		column = "substr(c.time,1,10)"
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
	order := column + " DESC"
	if group != "day" {
		order = "COUNT(*) DESC," + column
	}
	rows, err := a.readers.QueryContext(r.Context(), "SELECT "+column+",COUNT(*),COUNT(DISTINCT c.session),SUM(c.is_error)"+cards+" GROUP BY "+column+" ORDER BY "+order+" LIMIT 101", args...)
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
