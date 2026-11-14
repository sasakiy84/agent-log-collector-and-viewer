package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Independent full-session eligibility checks the incremental invalidation path.
const timelineCTE = `WITH eligible AS MATERIALIZED (
 SELECT id,session,parent,source,time,sequence,origin,kind,tool_name FROM events WHERE (?='' OR origin=?) AND session=?
) ` + timelineProjection

func projectionArgs(origin, session string) []any { return []any{origin, origin, session} }

func TestIncrementalProjectionMatchesFullProjection(t *testing.T) {
	a := setup(t)
	rng := rand.New(rand.NewSource(42))
	check := func() {
		t.Helper()
		for s := 0; s < 3; s++ {
			session := fmt.Sprint("codex:s", s)
			rows, err := a.DB.Query(timelineCTE+`SELECT id,time,end,sequence FROM timeline ORDER BY id`, projectionArgs("", session)...)
			if err != nil {
				t.Fatal(err)
			}
			type view struct {
				ID, Time, End string
				Sequence      int
			}
			want := []view{}
			for rows.Next() {
				var v view
				if err = rows.Scan(&v.ID, &v.Time, &v.End, &v.Sequence); err != nil {
					t.Fatal(err)
				}
				want = append(want, v)
			}
			rows.Close()
			rows, err = a.DB.Query("SELECT id,time,end,sequence FROM timeline_entries WHERE session=? ORDER BY id", session)
			if err != nil {
				t.Fatal(err)
			}
			got := []view{}
			for rows.Next() {
				var v view
				rows.Scan(&v.ID, &v.Time, &v.End, &v.Sequence)
				got = append(got, v)
			}
			rows.Close()
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s want=%+v got=%+v", session, want, got)
			}
			var count int
			err = a.DB.QueryRow("SELECT count FROM session_catalog WHERE session=?", session).Scan(&count)
			if len(want) == 0 {
				if err != sql.ErrNoRows {
					t.Fatal("empty session catalog retained", err)
				}
			} else if err != nil || count != len(want) {
				t.Fatal("wrong catalog count", count, len(want), err)
			}
		}
		for _, table := range []string{"card_counts", "timeline_counts"} {
			var total int
			if err := a.DB.QueryRow("SELECT COALESCE(SUM(count),0) FROM " + table).Scan(&total); err != nil {
				t.Fatal(err)
			}
			reference := "events"
			if table == "timeline_counts" {
				reference = "timeline_entries"
			}
			var expected int
			a.DB.QueryRow("SELECT COUNT(*) FROM " + reference).Scan(&expected)
			if total != expected {
				t.Fatal(table, total, expected)
			}
		}
	}
	for step := 0; step < 75; step++ {
		origin := "import"
		id := fmt.Sprint(rng.Intn(15))
		if step%3 == 0 {
			origin = "otel"
			id = fmt.Sprint("native", step)
		}
		e := recorded(t, id, origin, "codex", fmt.Sprint("s", rng.Intn(3)), "gen_ai.user.message", map[string]any{"event.id": fmt.Sprint(rng.Intn(5)), "message_id": fmt.Sprint(rng.Intn(5)), "prompt": fmt.Sprint("step", step)})
		e.Sequence = step
		e.Time = timestamp(time.Date(2026, 11, 14, 0, 0, step, 0, time.UTC))
		if step%11 == 0 {
			e.Time = "1970-01-01T00:00:00.000000000Z"
		}
		save(t, a, e)
		check()
	}
}
func TestReadModelCardsAreBoundedAndOriginalsLazy(t *testing.T) {
	a := setup(t)
	large := strings.Repeat("payload-", 20000)
	e := recorded(t, "large", "import", "codex", "large", "gen_ai.tool.result", map[string]any{"output": large, "success": large, "custom.attribute": large})
	save(t, a, e)
	response := archiveReq(a, "GET", "/api/session?id=codex:large", "")
	if response.Code != 200 || response.Body.Len() > 20000 {
		t.Fatal("large page payload", response.Code, response.Body.Len())
	}
	var page SessionPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || len(page.Events[0].Records[0].Raw) != 0 {
		t.Fatal("raw JSON sent on initial load")
	}
	if len(display(page.Events[0].Detail.Attributes["success"])) > 5 {
		t.Fatal("unbounded success attribute")
	}
	var full TimelineEvent
	getJSON(t, a, "/api/event?session=codex:large&id="+url.QueryEscape(e.ID), &full)
	if !bytes.Equal(full.Records[0].Raw, e.Raw) || full.Detail.Attributes["custom.attribute"] != large {
		t.Fatal("original attributes lost")
	}
	if w := archiveReq(a, "GET", "/api/event?session=codex:other&id="+url.QueryEscape(e.ID), ""); w.Code != 404 {
		t.Fatal("cross-session event leak", w.Code)
	}
}
func TestWALReadersDoNotWaitForIngestionTransaction(t *testing.T) {
	a := setup(t)
	save(t, a, oldEvent(t, "one", "old"))
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE events SET parent='pending' WHERE session='codex:old'"); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { w := archiveReq(a, "GET", "/api/session?id=codex:old", ""); done <- w.Code }()
	select {
	case status := <-done:
		if status != 200 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("read blocked by the write pool")
	}
	if _, err = a.readers.Exec("DELETE FROM events"); err == nil {
		t.Fatal("reader pool is writable")
	}
}
func TestIndexedReadModelRestartAndDeletion(t *testing.T) {
	a := setup(t)
	e := oldEvent(t, "one", "old")
	save(t, a, e)
	path := a.dbPath
	a.Close()
	b, err := Open(path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var summaries []SessionSummary
	getJSON(t, b, "/api/sessions", &summaries)
	if len(summaries) != 1 || summaries[0].Count != 1 {
		t.Fatal(summaries)
	}
	tx, err := b.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM events WHERE id=?", e.ID); err == nil {
		err = refreshTimelines(context.Background(), tx)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	getJSON(t, b, "/api/sessions", &summaries)
	if len(summaries) != 0 {
		t.Fatal("deleted summary retained")
	}
}

func TestLazyInspectorResolvesCardAfterLateMerge(t *testing.T) {
	a := setup(t)
	native := recorded(t, "native", "otel", "claude", "s", "user_prompt", map[string]any{"event_id": "shared", "prompt": "native"})
	save(t, a, native)
	p := page(t, a, "claude:s", "", 0)
	oldID := p.Events[0].ID
	imported := recorded(t, "imported", "import", "claude", "s", "gen_ai.user.message", map[string]any{"event.id": "shared", "prompt": "imported"})
	save(t, a, imported)
	var current TimelineEvent
	getJSON(t, a, "/api/event?session=claude:s&id="+url.QueryEscape(oldID), &current)
	if current.ID != imported.ID || len(current.Records) != 2 {
		t.Fatal("old card cannot resolve current group", current)
	}
	getJSON(t, a, "/api/event?session=claude:s&origin=otel&id="+url.QueryEscape(oldID), &current)
	if current.ID != native.ID || len(current.Records) != 1 {
		t.Fatal("origin isolation lost", current)
	}
}
