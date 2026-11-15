package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Raw payloads stay in events. These bounded presentations and indexed timeline
// rows are rebuilt transactionally on ingestion, never on an ordinary GET.

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
func boundedValue(v any) any {
	switch x := v.(type) {
	case string:
		return clip(x, 512)
	case []string:
		out := []string{}
		for _, s := range x[:min(8, len(x))] {
			out = append(out, clip(s, 128))
		}
		return out
	case []any:
		out := []any{}
		for _, v := range x[:min(8, len(x))] {
			out = append(out, clip(display(v), 128))
		}
		return out
	default:
		if v == nil {
			return nil
		}
		return clip(display(v), 512)
	}
}
func cardDetail(d Detail) Detail {
	d.Title = clip(d.Title, 240)
	d.Name = clip(d.Name, 160)
	blocks := []Block{}
	budget := 6000
	for _, b := range d.Blocks {
		if len(blocks) == 8 || budget <= 0 {
			break
		}
		b.Text = clip(b.Text, min(2000, budget))
		budget -= len(b.Text)
		b.Label = clip(b.Label, 80)
		blocks = append(blocks, b)
	}
	d.Blocks = blocks
	fields := [][2]string{}
	for _, f := range d.Fields[:min(16, len(d.Fields))] {
		fields = append(fields, [2]string{clip(f[0], 80), clip(f[1], 256)})
	}
	d.Fields = fields
	success := d.Attributes["success"]
	d.Attributes = map[string]any{}
	switch v := success.(type) {
	case bool:
		d.Attributes["success"] = v
	case string:
		if v == "true" || v == "false" {
			d.Attributes["success"] = v
		}
	}
	d.Conflicts = nil
	d.Model = boundedValue(d.Model)
	d.Host = boundedValue(d.Host)
	// Keep real paths exact, while arbitrary telemetry values stay bounded.
	d.Cwd = directoryPath(d.Cwd)
	d.Project = boundedValue(d.Project)
	d.Tags = boundedValue(d.Tags)
	return d
}
func summaryDetail(d Detail) Detail {
	d = cardDetail(d)
	for i := range d.Blocks {
		d.Blocks[i].Text = clip(d.Blocks[i].Text, 256)
	}
	d.Fields = nil
	d.Attributes = nil
	d.Conflicts = nil
	return d
}
func storeCard(ctx context.Context, tx *sql.Tx, e Event, d Detail, doc document) error {
	if err := storeSkillFacts(ctx, tx, e, d); err != nil {
		return err
	}
	if err := storeAgentData(ctx, tx, e, d); err != nil {
		return err
	}
	presentation, err := json.Marshal(cardDetail(d))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_cards(id,session,origin,source,parent,time,sequence,is_user,signal,trace,span,parent_span,detail,prompt_title,activity,kind,is_error,turn,presentation_ready,conversation,has_skill) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,EXISTS(SELECT 1 FROM skill_events WHERE event_id=?)) ON CONFLICT(id) DO UPDATE SET
 session=excluded.session,origin=excluded.origin,source=excluded.source,parent=excluded.parent,time=excluded.time,sequence=excluded.sequence,
 is_user=excluded.is_user,signal=excluded.signal,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,detail=excluded.detail,prompt_title=excluded.prompt_title,activity=excluded.activity,kind=excluded.kind,is_error=excluded.is_error,turn=excluded.turn,presentation_ready=1,conversation=excluded.conversation,has_skill=excluded.has_skill`,
		e.ID, e.Session, e.Origin, e.Source, e.Parent, e.Time, e.Sequence, d.Kind == "user", e.Signal, e.Trace, e.Span, e.ParentSpan, presentation, promptTitle(e.Source, d), detailActivity(d), d.Kind, d.Error, d.Turn, detailConversation(d), e.ID)
	if err == nil {
		err = storeUsage(ctx, tx, e, doc, d)
	}
	if err == nil {
		err = storeModelFact(ctx, tx, e, doc, d)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM cards_dirty WHERE id=?", e.ID)
	}
	return err
}
func refreshCards(ctx context.Context, tx *sql.Tx) error {
	for {
		count, err := refreshCardsBatch(ctx, tx)
		if err != nil || count == 0 {
			return err
		}
	}
}
func refreshCardsBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+qualifiedColumns("e")+" FROM cards_dirty d JOIN events e ON e.id=d.id LIMIT 200")
	if err != nil {
		return 0, err
	}
	for _, e := range batch {
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			return 0, err
		}
		if err = storeCard(ctx, tx, e, describe(e, doc), doc); err != nil {
			return 0, err
		}
		if err = storeSearch(ctx, tx, e, doc); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}

func (a *App) prepareReadModel(ctx context.Context) error {
	if err := a.drainBatches(ctx, "session cards", refreshCardsBatch); err != nil {
		return err
	}
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error { return clearChangedTimelineEntries(ctx, tx) }); err != nil {
		return err
	}
	var err error
	// Timeline upgrades also commit one session at a time. Completed sessions
	// leave the durable dirty queue, so a restart does not repeat the entire job.
	for {
		var session string
		err = a.DB.QueryRowContext(ctx, "SELECT session FROM read_model_dirty d WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=d.session) LIMIT 1").Scan(&session)
		if errors.Is(err, sql.ErrNoRows) {
			return inTransaction(ctx, a.DB, func(tx *sql.Tx) error { return refreshUsage(ctx, tx) })
		}
		if err != nil {
			return err
		}
		err = inTransaction(ctx, a.DB, func(tx *sql.Tx) error { return refreshTimeline(ctx, tx, session) })
		if err == nil {
			err = a.pauseMaintenance(ctx)
		}
		if err != nil {
			return err
		}
	}
}

func refreshTimelines(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT d.session FROM read_model_dirty d WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=d.session)")
	if err != nil {
		return err
	}
	sessions := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		sessions = append(sessions, id)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	// A moved event may otherwise collide with its old session's view ID when
	// the destination sorts before the source. Remove changed representatives first.
	if err = clearChangedTimelineEntries(ctx, tx); err != nil {
		return err
	}
	for _, id := range sessions {
		if err = refreshTimeline(ctx, tx, id); err != nil {
			return err
		}
	}
	return refreshUsage(ctx, tx)
}

func clearChangedTimelineEntries(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM timeline_entries WHERE id IN (SELECT d.id FROM read_model_dirty d WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=d.session))")
	return err
}
func refreshTimeline(ctx context.Context, tx *sql.Tx, session string) error {
	var imports, otel int
	var iStart, iEnd, oStart, oEnd string
	err := tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT SUM(count) FROM card_counts WHERE session=? AND origin='import'),0),
 COALESCE((SELECT SUM(count) FROM card_counts WHERE session=? AND origin='otel'),0),
 COALESCE((SELECT time FROM event_cards WHERE session=? AND origin='import' ORDER BY time LIMIT 1),''),
 COALESCE((SELECT time FROM event_cards WHERE session=? AND origin='import' ORDER BY time DESC LIMIT 1),''),
 COALESCE((SELECT time FROM event_cards WHERE session=? AND origin='otel' ORDER BY time LIMIT 1),''),
 COALESCE((SELECT time FROM event_cards WHERE session=? AND origin='otel' ORDER BY time DESC LIMIT 1),'')`, session, session, session, session, session, session).Scan(&imports, &otel, &iStart, &iEnd, &oStart, &oEnd)
	if err != nil {
		return err
	}
	var wasMixed bool
	err = tx.QueryRowContext(ctx, "SELECT import_count>0 AND otel_count>0 FROM session_catalog WHERE session=?", session).Scan(&wasMixed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	mixed := imports > 0 && otel > 0
	if mixed || wasMixed {
		// Changes can alter a counterpart's ambiguity, including partners of former
		// partners. Follow the complete shared-ID component; unrelated events in a
		// long mixed session never need to be regrouped.
		_, err = tx.ExecContext(ctx, `WITH RECURSIVE affected(id) AS (
   SELECT id FROM read_model_dirty WHERE session=? UNION
   SELECT neighbor.event_id FROM affected a CROSS JOIN events center ON center.id=a.id
   CROSS JOIN event_keys own ON own.event_id=a.id CROSS JOIN event_keys neighbor ON neighbor.key=own.key
   CROSS JOIN events e ON e.id=neighbor.event_id WHERE e.session=? AND e.source=center.source
  ) INSERT INTO read_model_dirty SELECT ?,id FROM affected WHERE true ON CONFLICT DO NOTHING`, session, session, session)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM timeline_entries WHERE id IN (SELECT e.id FROM read_model_dirty d CROSS JOIN timeline_entries e ON e.id=d.id WHERE d.session=? AND e.session=?)", session, session); err != nil {
			return err
		}
		query := componentCTE +
			`INSERT INTO timeline_entries(id,session,source,parent,time,end,sequence,is_user,import_id,otel_id) SELECT t.id,t.session,t.source,t.parent,t.time,t.end,t.sequence,t.is_user,
    COALESCE(MAX(CASE WHEN m.origin='import' THEN m.id END),''),COALESCE(MAX(CASE WHEN m.origin='otel' THEN m.id END),'')
    FROM timeline t JOIN members m ON m.view_id=t.id GROUP BY t.id`
		args := []any{session, session}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}

	} else {
		// The usual single-source append updates only changed rows, rather than
		// repeatedly rebuilding the entire session during a historical import.
		if _, err = tx.ExecContext(ctx, "DELETE FROM timeline_entries WHERE id IN (SELECT e.id FROM read_model_dirty d CROSS JOIN timeline_entries e ON e.id=d.id WHERE d.session=? AND e.session=?)", session, session); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO timeline_entries(id,session,source,parent,time,end,sequence,is_user,import_id,otel_id) SELECT c.id,c.session,c.source,c.parent,c.time,c.time,c.sequence,c.is_user,
   CASE WHEN c.origin='import' THEN c.id ELSE '' END,CASE WHEN c.origin='otel' THEN c.id ELSE '' END
   FROM read_model_dirty d CROSS JOIN event_cards c ON c.id=d.id WHERE d.session=? AND c.session=?`, session, session)
		if err != nil {
			return err
		}
	}
	if imports+otel == 0 {
		_, err = tx.ExecContext(ctx, "DELETE FROM session_catalog WHERE session=?", session)
	} else {
		detail, e := catalogPreview(ctx, tx, session)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO session_catalog SELECT ?,MAX(source),MAX(parent),
 (SELECT count FROM timeline_counts WHERE session=?),
 (SELECT time FROM timeline_entries WHERE session=? ORDER BY time LIMIT 1),
 (SELECT end FROM timeline_entries WHERE session=? ORDER BY end DESC LIMIT 1),?,?,?,?,?,?,? FROM card_counts WHERE session=?
 ON CONFLICT(session) DO UPDATE SET source=excluded.source,parent=excluded.parent,count=excluded.count,start=excluded.start,end=excluded.end,
 import_count=excluded.import_count,import_start=excluded.import_start,import_end=excluded.import_end,otel_count=excluded.otel_count,otel_start=excluded.otel_start,otel_end=excluded.otel_end,preview=excluded.preview`,
			session, session, session, session, imports, iStart, iEnd, otel, oStart, oEnd, detail, session)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO timeline_errors
 SELECT t.id,t.session FROM read_model_dirty d CROSS JOIN timeline_entries t ON t.id=d.id
 WHERE d.session=? AND EXISTS(SELECT 1 FROM event_errors WHERE id IN(t.import_id,t.otel_id))`, session)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM read_model_dirty WHERE session=?", session)
	}
	return err
}

const cardColumns = "id,session,origin,source,parent,time,sequence,signal,trace,span,parent_span,detail"

func qualifiedCardColumns(alias string) string {
	cols := strings.Split(cardColumns, ",")
	for i := range cols {
		cols[i] = alias + "." + cols[i]
	}
	return strings.Join(cols, ",")
}

type timelineOrder int

const (
	timelineChronological timelineOrder = iota
	timelinePreview
	timelineLatest
)

func readTimeline(ctx context.Context, tx *sql.Tx, session, origin string, limit, off int, mode timelineOrder) ([]TimelineEvent, error) {
	return readFilteredTimeline(ctx, tx, session, origin, limit, off, mode, eventFilter{})
}

func readFilteredTimeline(ctx context.Context, tx *sql.Tx, session, origin string, limit, off int, mode timelineOrder, filter eventFilter) ([]TimelineEvent, error) {
	order := "time,sequence,id"
	if mode == timelinePreview {
		order = "is_user DESC," + order
	} else if mode == timelineLatest {
		order = "time DESC,sequence DESC,id DESC"
		off = 0
	}
	var rows *sql.Rows
	var err error
	if origin != "" {
		query := "SELECT id," + cardColumns + " FROM event_cards WHERE session=? AND origin=?" + filter.predicate("") + " ORDER BY " + order + " LIMIT ? OFFSET ?"
		if mode == timelineLatest {
			query = "SELECT * FROM (" + query + ") ORDER BY time,sequence,id"
		}
		rows, err = tx.QueryContext(ctx, query, session, origin, limit, off)
	} else {
		rows, err = tx.QueryContext(ctx, `SELECT p.id,`+qualifiedCardColumns("c")+` FROM (SELECT id,time,sequence,import_id,otel_id FROM timeline_entries WHERE session=?`+filter.predicate("")+` ORDER BY `+order+` LIMIT ? OFFSET ?) p JOIN event_cards c ON c.id=p.import_id OR c.id=p.otel_id ORDER BY p.time,p.sequence,p.id,c.origin,c.id`, session, limit, off)
	}
	if err != nil {
		return nil, err
	}
	return decodeTimeline(rows)
}

func decodeTimeline(rows *sql.Rows) ([]TimelineEvent, error) {
	defer rows.Close()
	var err error
	out := []TimelineEvent{}
	for rows.Next() {
		var id string
		var e Event
		var d Detail
		var encoded []byte
		if err = rows.Scan(&id, &e.ID, &e.Session, &e.Origin, &e.Source, &e.Parent, &e.Time, &e.Sequence, &e.Signal, &e.Trace, &e.Span, &e.ParentSpan, &encoded); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(encoded, &d); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, TimelineEvent{ID: id, Session: e.Session, Source: e.Source, Time: e.Time, Signal: e.Signal, Trace: e.Trace, Span: e.Span, ParentSpan: e.ParentSpan, Detail: d, Origins: []string{e.Origin}, Records: []Event{e}})
		} else {
			group := &out[len(out)-1]
			group.Detail = combineDetails(group.Detail, d, group.Origins[0], e.Origin)
			group.Origins = append(group.Origins, e.Origin)
			group.Records = append(group.Records, e)
			if validEventTime(e.Time) && (!validEventTime(group.Time) || e.Time < group.Time) {
				group.Time = e.Time
			}
			if group.Trace == "" && e.Trace != "" {
				group.Trace = e.Trace
				group.Span = e.Span
				group.ParentSpan = e.ParentSpan
				group.Signal = e.Signal
			}
		}
	}
	return out, rows.Err()
}
