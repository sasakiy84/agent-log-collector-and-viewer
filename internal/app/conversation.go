package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
)

// A range describes chronological placement, not a causal relationship to a
// turn. Sources without turn IDs therefore retain an honest, stable ordering.
type ProcessingGroup struct {
	After    string `json:"after"`
	Before   string `json:"before"`
	Count    int    `json:"count"`
	Activity int    `json:"activity"`
	Errors   int    `json:"errors"`
	Matches  int    `json:"matches,omitempty"`
}

type processingPage struct {
	Events []TimelineEvent `json:"events"`
	Next   string          `json:"next"`
}

type timelinePosition struct {
	Time     string
	Sequence int
	ID       string
}

func timelineTable(origin string) string {
	if origin != "" {
		return "event_cards"
	}
	return "timeline_entries"
}

func timelineScope(session, origin string) (string, []any) {
	query, args := "session=?", []any{session}
	if origin != "" {
		query += " AND origin=?"
		args = append(args, origin)
	}
	return query, args
}

func conversationPosition(ctx context.Context, tx *sql.Tx, session, origin, id string, conversation bool) (timelinePosition, error) {
	var p timelinePosition
	scope, args := timelineScope(session, origin)
	args = append(args, id, conversation)
	err := tx.QueryRowContext(ctx, "SELECT time,sequence,id FROM "+timelineTable(origin)+" WHERE "+scope+" AND id=? AND conversation=?", args...).Scan(&p.Time, &p.Sequence, &p.ID)
	return p, err
}

func positionBound(query string, args []any, op string, p timelinePosition) (string, []any) {
	return query + " AND (time,sequence,id)" + op + "(?,?,?)", append(args, p.Time, p.Sequence, p.ID)
}

func processingRange(ctx context.Context, tx *sql.Tx, session, origin, after, before string) (string, []any, error) {
	query, args := timelineScope(session, origin)
	query += " AND conversation=0"
	for _, bound := range []struct{ id, op string }{{after, ">"}, {before, "<"}} {
		if bound.id == "" {
			continue
		}
		p, err := conversationPosition(ctx, tx, session, origin, bound.id, true)
		if err != nil {
			return "", nil, err
		}
		query, args = positionBound(query, args, bound.op, p)
	}
	return query, args, nil
}

// Reuse the list's Japanese full-text, exact-ID and event-facet semantics when
// navigating a search result. IN subqueries do not load matching log bodies.
func processingMatch(ctx context.Context, origin, encoded string) (string, []any, error) {
	params, err := url.ParseQuery(encoded)
	if err != nil {
		return "", nil, err
	}
	f := parseSessionFilter(params)
	query, args, err := f.matchingCards(ctx)
	if err != nil {
		return "", nil, err
	}
	ids := "SELECT c.id" + query
	if origin != "" {
		return " AND id IN(" + ids + ")", args, nil
	}
	return " AND (import_id IN(" + ids + ") OR otel_id IN(" + ids + "))", append(append([]any{}, args...), args...), nil
}

// A search result may live on an old conversation page. Locate that page using
// metadata, while keeping the matching processing body closed until requested.
func conversationMatchPosition(ctx context.Context, tx *sql.Tx, session, origin, match string, limit, total int) (string, int, error) {
	query, args := timelineScope(session, origin)
	predicate, more, err := processingMatch(ctx, origin, match)
	if err != nil {
		return "", 0, err
	}
	var p timelinePosition
	var conversation bool
	err = tx.QueryRowContext(ctx, "SELECT time,sequence,id,conversation FROM "+timelineTable(origin)+" WHERE "+query+predicate+" ORDER BY time,sequence,id LIMIT 1", append(args, more...)...).Scan(&p.Time, &p.Sequence, &p.ID, &conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", pageOffset(total, total, limit), nil
	}
	if err != nil {
		return "", 0, err
	}
	query, args = timelineScope(session, origin)
	op := "<=" // A processing log is disclosed before the next conversation.
	if conversation {
		op = "<"
	}
	query, args = positionBound(query+" AND conversation=1", args, op, p)
	var preceding int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+timelineTable(origin)+" WHERE "+query, args...).Scan(&preceding)
	return p.ID, pageOffset(preceding, total, limit), err
}

func processingSummary(ctx context.Context, tx *sql.Tx, session, origin, after, before, matchPredicate string, matchArgs []any) (ProcessingGroup, error) {
	g := ProcessingGroup{After: after, Before: before}
	query, args, err := processingRange(ctx, tx, session, origin, after, before)
	if err != nil {
		return g, err
	}
	from := " FROM " + timelineTable(origin) + " WHERE " + query
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(activity),0),COALESCE(SUM(is_error),0)"+from, args...).Scan(&g.Count, &g.Activity, &g.Errors)
	if err == nil && matchPredicate != "" && g.Count > 0 {
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*)"+from+matchPredicate, append(args, matchArgs...)...).Scan(&g.Matches)
	}
	return g, err
}

func conversationGroups(ctx context.Context, tx *sql.Tx, page SessionPage, origin, match string) ([]ProcessingGroup, error) {
	groups := []ProcessingGroup{}
	matchPredicate, matchArgs := "", []any{}
	if match != "" {
		var err error
		matchPredicate, matchArgs, err = processingMatch(ctx, origin, match)
		if err != nil {
			return nil, err
		}
	}
	after := ""
	if len(page.Events) > 0 {
		first, err := conversationPosition(ctx, tx, page.ID, origin, page.Events[0].ID, true)
		if err != nil {
			return nil, err
		}
		query, args := timelineScope(page.ID, origin)
		query, args = positionBound(query+" AND conversation=1", args, "<", first)
		err = tx.QueryRowContext(ctx, "SELECT id FROM "+timelineTable(origin)+" WHERE "+query+" ORDER BY time DESC,sequence DESC,id DESC LIMIT 1", args...).Scan(&after)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	add := func(before string) error {
		group, err := processingSummary(ctx, tx, page.ID, origin, after, before, matchPredicate, matchArgs)
		if err == nil && group.Count > 0 {
			groups = append(groups, group)
		}
		after = before
		return err
	}
	for _, event := range page.Events {
		if err := add(event.ID); err != nil {
			return nil, err
		}
	}
	if page.Offset+len(page.Events) >= page.Count {
		if err := add(""); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

// The first request reads indexed counters only. Bounded card bodies are read
// here on expansion, with keyset pagination even for very large log ranges.
func (a *App) processing(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	session := r.URL.Query().Get("session")
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var locked bool
	err = tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM archive_locks WHERE session=?) FROM session_catalog WHERE session=?", session, session).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if locked {
		http.Error(w, "Session is being archived", 409)
		return
	}
	query, args, err := processingRange(r.Context(), tx, session, origin, r.URL.Query().Get("after"), r.URL.Query().Get("before"))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Conversation changed; reload the session", 409)
		return
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if r.URL.Query().Get("diagnostics") != "1" {
		query += " AND activity=1"
	}
	if r.URL.Query().Get("errors") == "1" {
		query += " AND is_error=1"
	}
	if r.URL.Query().Get("matches") == "1" {
		predicate, more, matchErr := processingMatch(r.Context(), origin, r.URL.Query().Get("match"))
		if matchErr != nil {
			http.Error(w, "Invalid search", 400)
			return
		}
		query += predicate
		args = append(args, more...)
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		p, e := conversationPosition(r.Context(), tx, session, origin, cursor, false)
		if e != nil {
			if !errors.Is(e, sql.ErrNoRows) {
				databaseError(w, r, e)
			} else {
				http.Error(w, "Logs changed; reopen processing details", 409)
			}
			return
		}
		query, args = positionBound(query, args, ">", p)
	}
	const size = 50
	var rows *sql.Rows
	if origin != "" {
		rows, err = tx.QueryContext(r.Context(), "SELECT id,"+cardColumns+" FROM event_cards WHERE "+query+" ORDER BY time,sequence,id LIMIT ?", append(args, size+1)...)
	} else {
		rows, err = tx.QueryContext(r.Context(), "SELECT p.id,"+qualifiedCardColumns("c")+" FROM (SELECT id,time,sequence,import_id,otel_id FROM timeline_entries WHERE "+query+" ORDER BY time,sequence,id LIMIT ?) p JOIN event_cards c ON c.id=p.import_id OR c.id=p.otel_id ORDER BY p.time,p.sequence,p.id,c.origin,c.id", append(args, size+1)...)
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	events, err := decodeTimeline(rows)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	out := processingPage{Events: events}
	if len(events) > size {
		out.Events = events[:size]
		out.Next = out.Events[size-1].ID
	}
	if err = attachSkills(r.Context(), tx, out.Events); err == nil {
		err = attachSubagents(r.Context(), tx, out.Events)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, out)
}
