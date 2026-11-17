package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type SessionSummary struct {
	ID      string       `json:"id"`
	Source  string       `json:"source"`
	Parent  string       `json:"parent"`
	Count   int          `json:"count"`
	Start   string       `json:"start"`
	End     string       `json:"end"`
	Preview *Detail      `json:"preview"`
	Usage   UsageSummary `json:"usage"`
}
type SessionPage struct {
	MatchID    string            `json:"match_id,omitempty"`
	Processing []ProcessingGroup `json:"processing,omitempty"`
	TotalCount int               `json:"total_count,omitempty"`
	View       string            `json:"view,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	SessionSummary
	Offset       int             `json:"offset"`
	Limit        int             `json:"limit"`
	Children     []string        `json:"children"`
	ChildrenNext string          `json:"children_next,omitempty"`
	Events       []TimelineEvent `json:"events"`
	Skills       []SkillSummary  `json:"skills,omitempty"`
}

func (a *App) sessions(w http.ResponseWriter, r *http.Request) {
	a.sessionList(w, r, false)
}

func (a *App) exportSessions(w http.ResponseWriter, r *http.Request) {
	a.sessionList(w, r, true)
}

func (a *App) sessionList(w http.ResponseWriter, r *http.Request, forExport bool) {
	filter := parseSessionFilter(r.URL.Query())
	if forExport {
		filter = exportSessionFilter(r.URL.Query())
	}
	query, args, err := filter.sql(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Hide child sessions before counting and paging; their logs remain
	// accessible through the parent's links and the session detail endpoints.
	// Length avoids an equality lookup on catalog_children that sorts every
	// root session instead of paging through the existing recent-order indexes.
	if !forExport {
		query += " AND length(s.parent)=0"
	}
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var total int
	var records int64
	if forExport {
		err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*),COALESCE(SUM(s.import_count+s.otel_count),0)"+query, args...).Scan(&total, &records)
	} else {
		err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*)"+query, args...).Scan(&total)
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	off := pageOffset(offset(r), total, 200)
	args = append(args, off)
	countColumn := "s.count"
	if forExport {
		countColumn = "s.import_count+s.otel_count"
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT s.session,s.source,s.parent,"+countColumn+",s.start,s.end,s.preview,"+usageColumn+query+" ORDER BY s.end DESC,s.session LIMIT 201 OFFSET ?", args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	out := []SessionSummary{}
	for rows.Next() {
		var summary SessionSummary
		var encoded []byte
		var usage string
		if err = rows.Scan(&summary.ID, &summary.Source, &summary.Parent, &summary.Count, &summary.Start, &summary.End, &encoded, &usage); err != nil {
			break
		}
		var detail Detail
		if err = json.Unmarshal(encoded, &detail); err != nil {
			break
		}
		summary.Preview = &detail
		if summary.Usage, err = decodeUsage(usage); err != nil {
			break
		}
		out = append(out, summary)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		databaseError(w, r, errors.Join(err, rowErr))
		return
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("X-Page-Offset", strconv.Itoa(off))
	if forExport {
		w.Header().Set("X-Record-Count", strconv.FormatInt(records, 10))
	}
	write(w, out)
}

// Clamp stale or arbitrary offsets to a real page boundary.
func pageOffset(off, count, limit int) int {
	return min(off/limit, max(0, count-1)/limit) * limit
}
func offset(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if n < 0 {
		return 0
	}
	return n
}
func eventOrigin(r *http.Request) (string, bool) {
	origin := r.URL.Query().Get("origin")
	return origin, origin == "" || origin == "import" || origin == "otel"
}
func (a *App) session(w http.ResponseWriter, r *http.Request) {
	filter, err := parseEventFilter(r.URL.Query().Get("view"), r.URL.Query().Get("kind"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	id := r.URL.Query().Get("id")
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM archive_locks WHERE session=?)", id).Scan(&locked); err != nil {
		databaseError(w, r, err)
		return
	}
	if locked {
		http.Error(w, "Session is being archived", 409)
		return
	}
	columns := "count,start,end"
	if origin != "" {
		columns = origin + "_count," + origin + "_start," + origin + "_end"
	}
	page := SessionPage{SessionSummary: SessionSummary{ID: id}, Children: []string{}}
	var preview []byte
	var usage string
	err = tx.QueryRowContext(r.Context(), "SELECT source,parent,"+columns+",preview,COALESCE((SELECT summary FROM usage_summary u WHERE u.session=session_catalog.session AND u.origin=?),'{}') FROM session_catalog WHERE session=?", origin, id).Scan(&page.Source, &page.Parent, &page.Count, &page.Start, &page.End, &preview, &usage)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if page.Usage, err = decodeUsage(usage); err != nil {
		databaseError(w, r, err)
		return
	}
	if r.URL.Query().Get("view") != "" || filter.Kind != "" {
		page.TotalCount = page.Count
		page.View = r.URL.Query().Get("view")
		page.Kind = r.URL.Query().Get("kind")
		// Diagnostics without a type filter has exactly the catalog's count.
		// Do not recount a large history that this request has not narrowed.
		if filter.Activity || filter.Kind != "" {
			page.Count, err = filteredTimelineCount(r.Context(), tx, id, origin, filter)
			if err != nil {
				databaseError(w, r, err)
				return
			}
		}
	}
	page.Offset = offset(r)
	page.Limit = 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 200 {
			http.Error(w, "Invalid page size", 400)
			return
		}
		page.Limit = n
	}
	page.Offset = pageOffset(page.Offset, page.Count, page.Limit)
	order, size := timelineChronological, page.Limit+1
	position := r.URL.Query().Get("position")
	if position != "" && position != "latest" {
		http.Error(w, "Invalid position", 400)
		return
	}
	if position == "latest" {
		page.Offset = max(0, page.Count-1) / page.Limit * page.Limit
		order = timelineLatest
		size = page.Count - page.Offset
	}
	if match := r.URL.Query().Get("match"); match != "" && position == "latest" && r.URL.Query().Get("view") == "conversation" {
		page.MatchID, page.Offset, err = conversationMatchPosition(r.Context(), tx, id, origin, match, page.Limit, page.Count)
		if err != nil {
			http.Error(w, "Unable to apply search conditions", 400)
			return
		}
		order, size = timelineChronological, page.Limit+1
	}
	page.Events, err = readFilteredTimeline(r.Context(), tx, id, origin, size, page.Offset, order, filter)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if r.URL.Query().Get("view") == "conversation" {
		page.Events = page.Events[:min(len(page.Events), page.Limit)]
		page.Processing, err = conversationGroups(r.Context(), tx, page, origin, r.URL.Query().Get("match"))
		if err != nil {
			databaseError(w, r, err)
			return
		}
		for i := range page.Events {
			page.Events[i].Detail.Blocks = conversationBlocks(page.Events[i].Detail, page.Source)
			page.Events[i].Detail.Fields = [][2]string{}
		}
	}
	if origin == "" {
		var detail Detail
		if err = json.Unmarshal(preview, &detail); err == nil {
			page.Preview = &detail
		}
	} else {
		var detail Detail
		detail, err = sessionPreview(r.Context(), tx, id, origin)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		} else if err == nil {
			page.Preview = &detail
		}
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	children, err := readChildren(r.Context(), tx, id, "")
	if err != nil {
		databaseError(w, r, err)
		return
	}
	page.Children, page.ChildrenNext = children.Children, children.Next
	page.Skills, err = sessionSkills(r.Context(), tx, id, origin)
	if err == nil && r.URL.Query().Get("view") != "conversation" {
		err = attachSkills(r.Context(), tx, page.Events)
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if r.URL.Query().Get("view") != "conversation" {
		if err = attachModelCalls(r.Context(), tx, page.Events, origin); err == nil {
			err = attachSubagents(r.Context(), tx, page.Events)
		}
		if err != nil {
			databaseError(w, r, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, page)
}

// Full attributes and original JSON are fetched only for the inspected card.
func (a *App) event(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", 400)
		return
	}
	session, id := r.URL.Query().Get("session"), r.URL.Query().Get("id")
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	// A native card may gain an imported counterpart after the page was loaded.
	// Resolve its old raw ID to the current group without requiring a refresh.
	if origin == "" {
		var current string
		err = tx.QueryRowContext(r.Context(), `SELECT id FROM timeline_entries WHERE id=? AND session=?
   UNION ALL SELECT id FROM timeline_entries WHERE otel_id=? AND import_id<>'' AND otel_id<>'' AND session=? LIMIT 1`, id, session, id, session).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			databaseError(w, r, err)
			return
		}
		id = current
	}
	query := "SELECT " + qualifiedColumns("e") + " FROM events e WHERE e.session=? AND "
	args := []any{session}
	if origin != "" {
		query += "e.id=? AND e.origin=?"
		args = append(args, id, origin)
	} else {
		query += `e.id IN (SELECT import_id FROM timeline_entries WHERE session=? AND id=? UNION ALL SELECT otel_id FROM timeline_entries WHERE session=? AND id=?)`
		args = append(args, session, id, session, id)
	}
	rows, err := tx.QueryContext(r.Context(), query, args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	records := []Event{}
	for rows.Next() {
		e, eErr := scanEvent(rows)
		if eErr != nil {
			err = eErr
			break
		}
		records = append(records, e)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		databaseError(w, r, errors.Join(err, rowErr))
		return
	}
	if len(records) == 0 {
		http.NotFound(w, r)
		return
	}
	event, err := projectEvent(id, records)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	linked := []TimelineEvent{event}
	if err = attachModelCalls(r.Context(), tx, linked, origin); err != nil {
		databaseError(w, r, err)
		return
	}
	if err = attachSubagents(r.Context(), tx, linked); err != nil {
		databaseError(w, r, err)
		return
	}
	event = linked[0]
	if err = attachSkills(r.Context(), tx, linked); err != nil {
		databaseError(w, r, err)
		return
	}
	event = linked[0]
	if err = attachToolPair(r.Context(), tx, &event, origin); err != nil {
		databaseError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, event)
}
