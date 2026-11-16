package app

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Selections in one facet are ORed; independent facets are ANDed. Times use
// [from,to), canonical UTC nanoseconds, matching the stored event timestamps.
type searchFacets struct {
	Projects, Sources, Origins, Kinds []string
	From, To                          string
	Err                               error
}

func parseSearchFacets(q url.Values) searchFacets {
	f := searchFacets{}
	if mode := q.Get("text_mode"); mode != "" && mode != "all" && mode != "any" {
		f.Err = fmt.Errorf("Invalid text match mode")
	}
	parse := func(key string, allowed []string, normalize func(string) string) []string {
		out := []string{}
		if len(q[key]) > 100 {
			f.Err = fmt.Errorf("Too many %s selections (maximum 100)", key)
			return out
		}
		for _, v := range q[key] {
			if normalize != nil {
				v = normalize(v)
			}
			if len(v) > 512 || allowed != nil && !slices.Contains(allowed, v) {
				f.Err = fmt.Errorf("Invalid %s selection", key)
				return out
			}
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		return out
	}
	f.Projects = parse("project", nil, canonicalProjectID)
	f.Sources = parse("source", []string{"codex", "claude", "copilot", "other", ""}, nil)
	f.Sources = slices.DeleteFunc(f.Sources, func(s string) bool { return s == "" })
	f.Origins = parse("origin", []string{"otel", "import"}, nil)
	f.Kinds = parse("event_kind", []string{"conversation", "tool", "error", "model", "metric", "skill", "agent", "event"}, nil)
	for key, dst := range map[string]*string{"from": &f.From, "to": &f.To} {
		if !q.Has(key) {
			continue
		}
		raw := q.Get(key)
		t, err := time.Parse(time.RFC3339Nano, raw)
		if len(q[key]) != 1 || err != nil || t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
			f.Err = fmt.Errorf("Invalid %s: use an RFC3339 timestamp with a timezone", key)
			continue
		}
		*dst = t.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if f.From != "" && f.To != "" && f.From >= f.To {
		f.Err = fmt.Errorf("The start time must be earlier than the end time")
	}
	return f
}
func sqlSelections(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "", nil
	}
	marks, args := []string{}, []any{}
	for _, v := range values {
		marks = append(marks, "?")
		args = append(args, v)
	}
	return " AND " + column + " IN(" + strings.Join(marks, ",") + ")", args
}
func (f searchFacets) sessionSQL() (string, []any) {
	a, args := sqlSelections("s.project_key", f.Projects)
	b, more := sqlSelections("s.source", f.Sources)
	return a + b, append(args, more...)
}
func (f searchFacets) evidenceSQL(alias string) (string, []any) {
	query, args := sqlSelections(alias+".origin", f.Origins)
	for _, bound := range []struct{ value, op string }{{f.From, ">="}, {f.To, "<"}} {
		if bound.value != "" {
			query += " AND " + alias + ".time" + bound.op + "?"
			args = append(args, bound.value)
		}
	}
	return query, args
}
func (f searchFacets) hasEvents() bool {
	return f.From != "" || f.To != "" || len(f.Origins) > 0 || len(f.Kinds) > 0
}

// The FTS posting lookup remains the driver when text is present. Additional
// event predicates join by card primary key, so words outside the selected
// window/type/origin cannot accidentally satisfy the same search.
func (f sessionFilter) matchingCards(ctx context.Context) (sqlSelection, error) {
	if f.Facets.Err != nil {
		return sqlSelection{}, f.Facets.Err
	}
	if f.TextMode != "" && f.TextMode != "all" && f.TextMode != "any" {
		return sqlSelection{}, fmt.Errorf("Invalid text match mode")
	}
	from := " FROM event_cards c"
	scope := f.Facets
	if f.Skill != "" {
		scope.From = ""
		scope.To = ""
	}
	query, args := scope.evidenceSQL("c")
	if f.Skill != "" {
		evidence, more := f.Facets.evidenceSQL("k")
		query += ` AND EXISTS(SELECT 1 FROM skill_events k WHERE k.event_id=c.id AND k.name=? AND k.plugin=?` + evidence + `)`
		args = append(args, f.Skill, f.Plugin)
		args = append(args, more...)
	}
	if len(f.Facets.Origins) == 0 {
		// Fixing origin lets cards_page use its session/origin/time range instead
		// of scanning every event of long-running sessions for bounded searches.
		query += " AND c.origin IN('otel','import')"
	}
	if len(f.Facets.Kinds) > 0 {
		predicates := []string{}
		for _, kind := range f.Facets.Kinds {
			ef, _ := parseEventFilter("", kind)
			predicates = append(predicates, "("+strings.TrimPrefix(ef.predicate("c"), " AND ")+")")
		}
		query += " AND (" + strings.Join(predicates, " OR ") + ")"
	}
	if f.Search != "" {
		match, err := searchMatchMode(ctx, f.Search, f.TextMode)
		if err != nil {
			return sqlSelection{}, err
		}
		if match == "" {
			query += " AND 0"
		} else {
			from = ` FROM event_search CROSS JOIN search_documents d ON d.rowid=event_search.rowid JOIN event_cards c ON c.id=d.event_id`
			query += " AND event_search MATCH ?"
			args = append(args, match)
		}
	}
	if f.IDValue != "" {
		query += " AND c.id IN(SELECT event_id FROM search_identifiers WHERE value=?"
		args = append(args, f.IDValue)
		if f.IDKey != "" {
			query += " AND key=?"
			args = append(args, f.IDKey)
		}
		query += ")"
	}
	if f.Exclude != "" {
		match, err := searchMatchMode(ctx, f.Exclude, "any")
		if err != nil {
			return sqlSelection{}, err
		}
		if match != "" {
			query += ` AND c.id NOT IN(SELECT d.event_id FROM event_search CROSS JOIN search_documents d ON d.rowid=event_search.rowid WHERE event_search MATCH ?)`
			args = append(args, match)
		}
	}
	return sqlSelection{from: from, where: query, args: args}, nil
}
