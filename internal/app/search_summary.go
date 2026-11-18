package app

import (
	"cmp"
	"net/http"
	"slices"
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
		cards.from = " FROM sessions s CROSS JOIN record_facts c ON c.session=s.session"
	} else {
		cards.from += " JOIN sessions s ON s.session=c.session"
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
	query := "SELECT " + column + ",c.session,1,c.is_error" + cards.sql()
	args := append(columnArgs, cards.args...)
	if filter.Search == "" && filter.Exclude == "" && filter.Skill == "" && filter.IDValue == "" && len(filter.Facets.Kinds) == 0 {
		query, args = indexedSearchSummary(group, column, filter.Facets, metadata)
	}
	rows, err := a.readers.QueryContext(r.Context(), query, args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer rows.Close()
	// Keep one counter per group and session. SQLite's GROUP BY sorter
	// materialized one entry per matching record and could spill to disk.
	type bucket struct {
		Key      string `json:"key"`
		Records  int64  `json:"records"`
		Sessions int64  `json:"sessions"`
		Errors   int64  `json:"errors"`
	}
	totals := map[string]*bucket{}
	seen := make(map[[2]string]struct{})
	for rows.Next() {
		var key, session string
		var records, errors int64
		if err = rows.Scan(&key, &session, &records, &errors); err != nil {
			break
		}
		if records == 0 {
			continue
		}
		total := totals[key]
		if total == nil {
			total = &bucket{Key: key}
			totals[key] = total
		}
		total.Records += records
		total.Errors += errors
		pair := [2]string{key, session}
		if _, ok := seen[pair]; !ok {
			total.Sessions++
			seen[pair] = struct{}{}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	groups := make([]*bucket, 0, len(totals))
	for _, total := range totals {
		groups = append(groups, total)
	}
	slices.SortFunc(groups, func(a, b *bucket) int {
		if group == "day" {
			return cmp.Compare(b.Key, a.Key)
		}
		if a.Records != b.Records {
			return cmp.Compare(b.Records, a.Records)
		}
		return cmp.Compare(a.Key, b.Key)
	})
	limited := len(groups) > 100
	if limited {
		groups = groups[:100]
	}
	write(w, map[string]any{"groups": groups, "group": group, "limited": limited, "timezone": "UTC"})
}

// Summarize indexed ranges before returning rows to Go. Each probe starts
// with session and origin, so even a long session can skip whole days.
func indexedSearchSummary(group, column string, facets searchFacets, metadata sqlSelection) (string, []any) {
	originSQL, originArgs := sqlSelections("o.origin", facets.Origins)
	facets.Origins = nil
	timeSQL, timeArgs := facets.evidenceSQL("c")
	const origins = "origins(origin) AS (VALUES('otel'),('import'))"
	const from = " FROM sessions s CROSS JOIN origins o"
	const originRange = " WHERE c.session=s.session AND c.origin=o.origin"
	const seed = " FROM record_facts c" + originRange
	const errorSeed = " FROM record_facts c INDEXED BY cards_error_page" + originRange
	where := " WHERE length(s.parent)=0" + metadata.where + originSQL
	args := slices.Concat(metadata.args, originArgs)

	switch group {
	case "day":
		const day = "substr(d.last_time,1,10)"
		const scan = " FROM record_facts c WHERE c.session=d.session AND c.origin=d.origin"
		const rangeSQL = scan + " AND c.time>=" + day + " AND c.time<=d.last_time"
		const latest = " ORDER BY c.time DESC LIMIT 1)"
		return "WITH RECURSIVE " + origins + `, daily(session,origin,last_time) AS (
 SELECT s.session,o.origin,(SELECT c.time` + seed + timeSQL + latest + from + where + `
 UNION ALL
 SELECT d.session,d.origin,(SELECT c.time` + scan + " AND c.time<" + day + timeSQL + latest + `
 FROM daily d WHERE d.last_time IS NOT NULL
)
SELECT ` + day + `,d.session,
 (SELECT COUNT(*)` + rangeSQL + timeSQL + `),
 (SELECT COUNT(*)` + rangeSQL + timeSQL + ` AND c.is_error=1)
FROM daily d WHERE d.last_time IS NOT NULL`,
			slices.Concat(timeArgs, args, timeArgs, timeArgs, timeArgs)
	case "kind":
		// For one session and origin, cards_kind_page already orders records
		// by kind. Each JSON entry holds the total from one indexed run.
		return "WITH " + origins + `
SELECT kinds.key,s.session,kinds.value,
 (SELECT COUNT(*)` + errorSeed + timeSQL + ` AND c.is_error=1 AND c.kind=kinds.key)
` + from + `
CROSS JOIN json_each((
 SELECT json_group_object(kind,records) FROM (
  SELECT c.kind,COUNT(*) AS records
  FROM record_facts c INDEXED BY cards_kind_page
  WHERE c.session=s.session AND c.origin=o.origin` + timeSQL + `
  GROUP BY c.kind
 )
)) kinds` + where, slices.Concat(timeArgs, timeArgs, args)
	default:
		// Project and source are constant for all records in a session.
		rangeSQL := seed + timeSQL
		return "WITH " + origins + " SELECT " + column + ",s.session,(SELECT COUNT(*)" + rangeSQL + "),(SELECT COUNT(*)" + rangeSQL + " AND c.is_error=1)" + from + where,
			slices.Concat(timeArgs, timeArgs, args)
	}
}
