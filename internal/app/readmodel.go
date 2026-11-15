package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
)

// Raw payloads stay in events. These bounded presentations and indexed timeline
// rows are rebuilt transactionally on ingestion, never on an ordinary GET.
const readModelSchema = `
CREATE TABLE IF NOT EXISTS event_cards(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,
 signal TEXT NOT NULL,trace TEXT NOT NULL,span TEXT NOT NULL,parent_span TEXT NOT NULL,detail BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS cards_page ON event_cards(session,origin,time,sequence,id);
CREATE INDEX IF NOT EXISTS cards_preview ON event_cards(session,origin,is_user DESC,time,sequence,id);
CREATE INDEX IF NOT EXISTS cards_directory ON event_cards(session,is_user DESC,time,sequence,id) WHERE json_type(detail,'$.cwd')='text' AND json_extract(detail,'$.cwd')<>'';
CREATE TABLE IF NOT EXISTS timeline_entries(
 id TEXT PRIMARY KEY,session TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,
 end TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,import_id TEXT NOT NULL,otel_id TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS timeline_page ON timeline_entries(session,time,sequence,id);
CREATE INDEX IF NOT EXISTS timeline_preview ON timeline_entries(session,is_user DESC,time,sequence,id);
CREATE UNIQUE INDEX IF NOT EXISTS timeline_pair_otel ON timeline_entries(otel_id) WHERE import_id<>'' AND otel_id<>'';
CREATE INDEX IF NOT EXISTS timeline_end ON timeline_entries(session,end DESC);
CREATE TABLE IF NOT EXISTS card_counts(session TEXT NOT NULL,origin TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(session,origin,source,parent)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS timeline_counts(session TEXT PRIMARY KEY,count INTEGER NOT NULL);
CREATE TRIGGER IF NOT EXISTS card_count_insert AFTER INSERT ON event_cards BEGIN
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;
CREATE TRIGGER IF NOT EXISTS card_count_delete AFTER DELETE ON event_cards BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;END;
CREATE TRIGGER IF NOT EXISTS card_count_update AFTER UPDATE ON event_cards BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;
CREATE TRIGGER IF NOT EXISTS timeline_count_insert AFTER INSERT ON timeline_entries BEGIN
 INSERT INTO timeline_counts VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET count=count+1;END;
CREATE TRIGGER IF NOT EXISTS timeline_count_delete AFTER DELETE ON timeline_entries BEGIN
 UPDATE timeline_counts SET count=count-1 WHERE session=OLD.session;
 DELETE FROM timeline_counts WHERE session=OLD.session AND count=0;END;
CREATE TABLE IF NOT EXISTS session_catalog(
 session TEXT PRIMARY KEY,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 import_count INTEGER NOT NULL,import_start TEXT NOT NULL,import_end TEXT NOT NULL,
 otel_count INTEGER NOT NULL,otel_start TEXT NOT NULL,otel_end TEXT NOT NULL,preview BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS catalog_recent ON session_catalog(end DESC,session);
CREATE INDEX IF NOT EXISTS catalog_source_recent ON session_catalog(source,end DESC,session);
CREATE INDEX IF NOT EXISTS catalog_children ON session_catalog(parent,session);
CREATE INDEX IF NOT EXISTS catalog_directory_recent ON session_catalog(COALESCE(json_extract(preview,'$.cwd'),''),end DESC,session);
CREATE TABLE IF NOT EXISTS read_model_dirty(session TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(session,id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS cards_dirty(id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE);
CREATE TRIGGER IF NOT EXISTS read_model_neighbors_UPDATE BEFORE UPDATE ON events WHEN EXISTS(SELECT 1 FROM event_keys WHERE event_id=OLD.id) BEGIN
 INSERT INTO read_model_dirty SELECT e.session,e.id FROM event_keys old JOIN event_keys neighbor ON neighbor.key=old.key
 JOIN events e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_neighbors_DELETE BEFORE DELETE ON events WHEN EXISTS(SELECT 1 FROM event_keys WHERE event_id=OLD.id) BEGIN
 INSERT INTO read_model_dirty SELECT e.session,e.id FROM event_keys old JOIN event_keys neighbor ON neighbor.key=old.key
 JOIN events e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_insert AFTER INSERT ON events BEGIN
 INSERT INTO read_model_dirty VALUES(NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO cards_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_update AFTER UPDATE ON events BEGIN
 INSERT INTO read_model_dirty VALUES(OLD.session,OLD.id) ON CONFLICT DO NOTHING;
 INSERT INTO read_model_dirty VALUES(NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO cards_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_delete AFTER DELETE ON events BEGIN
 INSERT INTO read_model_dirty VALUES(OLD.session,OLD.id) ON CONFLICT DO NOTHING;END;
`

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
func storeCard(ctx context.Context, tx *sql.Tx, e Event, d Detail) error {
	presentation, err := json.Marshal(cardDetail(d))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_cards VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
 session=excluded.session,origin=excluded.origin,source=excluded.source,parent=excluded.parent,time=excluded.time,sequence=excluded.sequence,
 is_user=excluded.is_user,signal=excluded.signal,trace=excluded.trace,span=excluded.span,parent_span=excluded.parent_span,detail=excluded.detail`,
		e.ID, e.Session, e.Origin, e.Source, e.Parent, e.Time, e.Sequence, d.Kind == "user", e.Signal, e.Trace, e.Span, e.ParentSpan, presentation)
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM cards_dirty WHERE id=?", e.ID)
	}
	return err
}
func refreshCards(ctx context.Context, tx *sql.Tx) error {
	for {
		rows, err := tx.QueryContext(ctx, "SELECT "+qualifiedColumns("e")+" FROM cards_dirty d JOIN events e ON e.id=d.id LIMIT 200")
		if err != nil {
			return err
		}
		batch := []Event{}
		size := 0
		for rows.Next() {
			e, eErr := scanEvent(rows)
			if eErr != nil {
				err = eErr
				break
			}
			batch = append(batch, e)
			size += len(e.Raw)
			if size >= 8<<20 {
				break
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			return errors.Join(err, rowErr)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, e := range batch {
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				return err
			}
			if err = storeCard(ctx, tx, e, describe(e, doc)); err != nil {
				return err
			}
			if err = storeSearch(ctx, tx, e, doc); err != nil {
				return err
			}
		}
	}
}
func (a *App) initReadModel() error {
	if _, err := a.DB.Exec(readModelSchema); err != nil {
		return err
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO cards_dirty SELECT e.id FROM events e LEFT JOIN event_cards c ON c.id=e.id WHERE c.id IS NULL;
 INSERT OR IGNORE INTO read_model_dirty SELECT e.session,e.id FROM events e JOIN cards_dirty d ON d.id=e.id;`)
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	if err != nil {
		return err
	}
	var remaining int
	if err = a.DB.QueryRow("SELECT COUNT(*) FROM cards_dirty").Scan(&remaining); err != nil {
		return err
	}
	if remaining > 0 {
		log.Printf("Preparing indexed session views for %d existing records (one-time upgrade)", remaining)
	}
	// Commit bounded batches so a restart can continue an interrupted upgrade.
	for remaining > 0 {
		tx, err = a.DB.Begin()
		if err != nil {
			return err
		}
		rows, e := tx.Query("SELECT " + qualifiedColumns("e") + " FROM cards_dirty d JOIN events e ON e.id=d.id LIMIT 200")
		if e != nil {
			tx.Rollback()
			return e
		}
		batch := []Event{}
		size := 0
		for rows.Next() {
			record, e := scanEvent(rows)
			if e != nil {
				err = e
				break
			}
			batch = append(batch, record)
			size += len(record.Raw)
			if size >= 8<<20 {
				break
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			tx.Rollback()
			return errors.Join(err, rowErr)
		}
		for _, record := range batch {
			doc, e := decodeDocument(record.Raw)
			if e != nil {
				err = e
				break
			}
			if err = storeCard(context.Background(), tx, record, describe(record, doc)); err != nil {
				break
			}
			if err = storeSearch(context.Background(), tx, record, doc); err != nil {
				break
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
		remaining -= len(batch)
		if len(batch) == 0 {
			return fmt.Errorf("presentation backfill made no progress")
		}
	}
	// Timeline upgrades also commit one session at a time. Completed sessions
	// leave the durable dirty queue, so a restart does not repeat the entire job.
	for {
		var session string
		err = a.DB.QueryRow("SELECT session FROM read_model_dirty d WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=d.session) LIMIT 1").Scan(&session)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		tx, err = a.DB.Begin()
		if err != nil {
			return err
		}
		if err = refreshTimeline(context.Background(), tx, session); err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
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
	if _, err = tx.ExecContext(ctx, "DELETE FROM timeline_entries WHERE id IN (SELECT d.id FROM read_model_dirty d WHERE NOT EXISTS(SELECT 1 FROM archive_locks l WHERE l.session=d.session))"); err != nil {
		return err
	}
	for _, id := range sessions {
		if err = refreshTimeline(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
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
			`INSERT INTO timeline_entries SELECT t.id,t.session,t.source,t.parent,t.time,t.end,t.sequence,t.is_user,
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
		_, err = tx.ExecContext(ctx, `INSERT INTO timeline_entries SELECT c.id,c.session,c.source,c.parent,c.time,c.time,c.sequence,c.is_user,
   CASE WHEN c.origin='import' THEN c.id ELSE '' END,CASE WHEN c.origin='otel' THEN c.id ELSE '' END
   FROM read_model_dirty d CROSS JOIN event_cards c ON c.id=d.id WHERE d.session=? AND c.session=?`, session, session)
		if err != nil {
			return err
		}
	}
	if imports+otel == 0 {
		_, err = tx.ExecContext(ctx, "DELETE FROM session_catalog WHERE session=?", session)
	} else {
		preview, e := readTimeline(ctx, tx, session, "", 1, 0, timelinePreview)
		if e != nil {
			return e
		}
		folder, e := sessionDirectory(ctx, tx, session)
		if e != nil {
			return e
		}
		summary := summaryDetail(preview[0].Detail)
		summary.Cwd = folder
		detail, e := json.Marshal(summary)
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
		query := "SELECT id," + cardColumns + " FROM event_cards WHERE session=? AND origin=? ORDER BY " + order + " LIMIT ? OFFSET ?"
		if mode == timelineLatest {
			query = "SELECT * FROM (" + query + ") ORDER BY time,sequence,id"
		}
		rows, err = tx.QueryContext(ctx, query, session, origin, limit, off)
	} else {
		rows, err = tx.QueryContext(ctx, `SELECT p.id,`+qualifiedCardColumns("c")+` FROM (SELECT id,time,sequence,import_id,otel_id FROM timeline_entries WHERE session=? ORDER BY `+order+` LIMIT ? OFFSET ?) p JOIN event_cards c ON c.id=p.import_id OR c.id=p.otel_id ORDER BY p.time,p.sequence,p.id,c.origin,c.id`, session, limit, off)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
