package app

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func finishProcessingHistory(t *testing.T, a *App) {
	t.Helper()
	for _, step := range []struct{ table, job string }{{"timeline_entries", processingTimelineJob}, {"event_cards", processingCardsJob}} {
		if err := a.runMaintenance(context.Background(), step.job, backfillProcessing(step.table), 0); err != nil {
			t.Fatal(err)
		}
	}
}
func assertProcessingBuckets(t *testing.T, a *App) {
	t.Helper()
	type key struct{ session, origin, minute string }
	read := func(query string) map[key][3]int {
		rows, err := a.DB.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[key][3]int{}
		for rows.Next() {
			var k key
			var n [3]int
			if err := rows.Scan(&k.session, &k.origin, &k.minute, &n[0], &n[1], &n[2]); err != nil {
				t.Fatal(err)
			}
			out[k] = n
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	actual := read("SELECT session,origin,minute,count,activity,errors FROM processing_buckets")
	expected := read(`SELECT session,'',substr(time,1,16),COUNT(*),SUM(activity),SUM(is_error) FROM timeline_entries WHERE conversation=0 GROUP BY session,substr(time,1,16)
 UNION ALL SELECT session,origin,substr(time,1,16),COUNT(*),SUM(activity),SUM(is_error) FROM event_cards WHERE conversation=0 GROUP BY session,origin,substr(time,1,16)`)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("processing buckets: got %v; want %v", actual, expected)
	}
}
func TestProcessingSummariesRetainExactRangesAndOrigins(t *testing.T) {
	a := setup(t)
	finishProcessingHistory(t, a)
	var events []Event
	base := time.Date(2026, 11, 16, 23, 58, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		for _, origin := range []string{"import", "otel"} {
			e := recorded(t, fmt.Sprintf("log-%s-%02d", origin, i), origin, "codex", "buckets", "runtime_event", map[string]any{"error": i%7 == 0})
			e.Time = base.Add(time.Duration(i/2) * time.Minute).Format("2006-01-02T15:04:05.000000000Z")
			e.Sequence = i
			events = append(events, e)
		}
	}
	save(t, a, events...)
	assertProcessingBuckets(t, a)
	for _, origin := range []string{"", "import", "otel"} {
		if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
			// Include equal timestamps, empty leading/trailing ranges and day rollover.
			var positions []*timelinePosition
			positions = append(positions, nil)
			for _, i := range []int{0, 1, 3, 6, 15, 23} {
				e := events[i*2]
				if origin == "otel" {
					e = events[i*2+1]
				}
				positions = append(positions, &timelinePosition{Time: e.Time, Sequence: e.Sequence, ID: e.ID})
			}
			for i, after := range positions {
				for j, before := range positions {
					if i > 0 && j > 0 && i > j {
						continue
					}
					var want, got [3]int
					for _, cached := range []bool{false, true} {
						query, args := processingCountSQL("codex:buckets", origin, after, before, cached)
						dest := &want
						if cached {
							dest = &got
						}
						if err := tx.QueryRow(query, args...).Scan(&dest[0], &dest[1], &dest[2]); err != nil {
							return err
						}
					}
					if got != want {
						t.Fatalf("origin=%s range=%d..%d cached=%v direct=%v", origin, i, j, got, want)
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Imported/native counterparts sharing an event ID must remain one logical
	// processing record, while retaining their independent origin summaries.
	one := recorded(t, "imported", "import", "codex", "buckets", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "call_id": "shared", "arguments": `{"path":"fixture.txt"}`})
	two := recorded(t, "native", "otel", "codex", "buckets", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "call_id": "shared", "arguments": `{"path":"fixture.txt"}`})
	save(t, a, one)
	save(t, a, two)
	assertProcessingBuckets(t, a)
	two.Time = base.Add(48 * time.Hour).Format("2006-01-02T15:04:05.000000000Z")
	save(t, a, two)
	assertProcessingBuckets(t, a)
	if _, err := a.DB.Exec("DELETE FROM events WHERE session='codex:buckets'"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	assertProcessingBuckets(t, a)
}
func TestProcessingBackfillResumesWithConcurrentChanges(t *testing.T) {
	a := setup(t)
	finishProcessingHistory(t, a)
	var events []Event
	for i := 0; i < 1005; i++ {
		events = append(events, recorded(t, fmt.Sprintf("a%04d", i), "otel", "codex", "rebuild", "runtime_event", nil))
	}
	save(t, a, events...)
	// Reproduce an upgrade: counters are absent and both cursors start before
	// historical rows. The API uses exact indexed counts until a scope is ready.
	if _, err := a.DB.Exec("DELETE FROM processing_buckets; UPDATE maintenance_jobs SET cursor='',complete=0,processed=0 WHERE name IN(?,?)", processingCardsJob, processingTimelineJob); err != nil {
		t.Fatal(err)
	}
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		next, n, err := backfillProcessing("timeline_entries")(context.Background(), tx, "")
		if err != nil {
			return err
		}
		if n != 1000 {
			t.Fatal(n)
		}
		_, err = tx.Exec("UPDATE maintenance_jobs SET cursor=?,processed=? WHERE name=?", next, n, processingTimelineJob)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var partial SessionPage
	getJSON(t, a, "/api/session?id=codex:rebuild&view=conversation", &partial)
	if len(partial.Processing) != 1 || partial.Processing[0].Count != 1005 {
		t.Fatal("partial history leaked into navigation", partial.Processing)
	}
	save(t, a, recorded(t, "a0500-late", "otel", "codex", "rebuild", "runtime_event", nil), recorded(t, "z-late", "otel", "codex", "rebuild", "runtime_event", nil))
	moved := events[0]
	moved.Session = "codex:moved"
	moved.Time = "2026-11-17T01:00:00.000000000Z"
	save(t, a, moved)
	changed := recorded(t, "a1002", "otel", "codex", "rebuild", "gen_ai.user.message", map[string]any{"prompt": "human question"})
	save(t, a, changed)
	if _, err := a.DB.Exec("DELETE FROM events WHERE id IN('a0001','a1004')"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	finishProcessingHistory(t, a)
	assertProcessingBuckets(t, a)
	// A subsequent restart must preserve completed counters and checkpoints.
	var count int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM processing_buckets").Scan(&count); err != nil || count < 1 {
		t.Fatal(count, err)
	}
	if err := a.startProcessingIndexer(); err != nil {
		t.Fatal(err)
	}
	assertProcessingBuckets(t, a)
}
