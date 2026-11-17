package app

import (
	"context"
	"database/sql"
	"encoding/json"
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
	selection, err := f.matchingCards(ctx)
	if err != nil {
		return "", nil, err
	}
	ids := "SELECT c.id" + selection.sql()
	args := selection.args
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

// Resolve only this page's logical positions, including merged import/OTEL
// entries. Their original card timestamps/sequences need not match the group.
func conversationPositions(ctx context.Context, tx *sql.Tx, page SessionPage, origin string) (map[string]timelinePosition, error) {
	positions := map[string]timelinePosition{}
	if len(page.Events) == 0 {
		return positions, nil
	}
	ids := make([]string, len(page.Events))
	for i, event := range page.Events {
		ids[i] = event.ID
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	scope, args := timelineScope(page.ID, origin)
	// JSON IDs drive primary-key probes, even if every row in a large session
	// is conversational. Do not walk that session just to resolve 200 IDs.
	rows, err := tx.QueryContext(ctx, "SELECT t.time,t.sequence,t.id FROM json_each(?) requested CROSS JOIN "+timelineTable(origin)+" t ON t.id=requested.value WHERE "+scope+" AND conversation=1", append([]any{string(encoded)}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p timelinePosition
		if err = rows.Scan(&p.Time, &p.Sequence, &p.ID); err != nil {
			return nil, err
		}
		positions[p.ID] = p
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(positions) != len(ids) {
		return nil, sql.ErrNoRows
	}
	return positions, nil
}

func conversationGroups(ctx context.Context, tx *sql.Tx, page SessionPage, origin, match string) ([]ProcessingGroup, error) {
	groups := []ProcessingGroup{}
	positions, err := conversationPositions(ctx, tx, page, origin)
	if err != nil {
		return nil, err
	}
	matchPredicate, matchArgs := "", []any{}
	if match != "" {
		var err error
		matchPredicate, matchArgs, err = processingMatch(ctx, origin, match)
		if err != nil {
			return nil, err
		}
	}
	// Reuse the few range shapes (leading, bounded, trailing) for this request.
	// Hundreds of gaps must not repeatedly parse the same count SQL.
	statements := map[string]*sql.Stmt{}
	defer func() {
		for _, stmt := range statements {
			stmt.Close()
		}
	}()
	count := func(query string, args []any, values ...any) error {
		stmt := statements[query]
		if stmt == nil {
			var err error
			stmt, err = tx.PrepareContext(ctx, query)
			if err != nil {
				return err
			}
			statements[query] = stmt
		}
		return stmt.QueryRowContext(ctx, args...).Scan(values...)
	}
	ready, err := processingSummaryReady(ctx, tx, origin)
	if err != nil {
		return nil, err
	}
	var after *timelinePosition
	if len(page.Events) > 0 {
		first := positions[page.Events[0].ID]
		query, args := timelineScope(page.ID, origin)
		query, args = positionBound(query+" AND conversation=1", args, "<", first)
		var previous timelinePosition
		err := tx.QueryRowContext(ctx, "SELECT time,sequence,id FROM "+timelineTable(origin)+" WHERE "+query+" ORDER BY time DESC,sequence DESC,id DESC LIMIT 1", args...).Scan(&previous.Time, &previous.Sequence, &previous.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			after = &previous
		}
	}
	add := func(before *timelinePosition) error {
		group := ProcessingGroup{}
		query, args := timelineScope(page.ID, origin)
		query += " AND conversation=0"
		if after != nil {
			group.After = after.ID
			query, args = positionBound(query, args, ">", *after)
		}
		if before != nil {
			group.Before = before.ID
			query, args = positionBound(query, args, "<", *before)
		}
		from := " FROM " + timelineTable(origin) + " WHERE " + query
		countSQL, countArgs := processingCountSQL(page.ID, origin, after, before, ready)
		err := count(countSQL, countArgs, &group.Count, &group.Activity, &group.Errors)
		if err == nil && matchPredicate != "" && group.Count > 0 {
			err = count("SELECT COUNT(*)"+from+matchPredicate, append(args, matchArgs...), &group.Matches)
		}
		if err == nil && group.Count > 0 {
			groups = append(groups, group)
		}
		after = before
		return err
	}
	for _, event := range page.Events {
		before := positions[event.ID]
		if err := add(&before); err != nil {
			return nil, err
		}
	}
	if page.Offset+len(page.Events) >= page.Count {
		if err := add(nil); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

// The first request reads indexed summary flags only. Bounded card bodies are read
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
