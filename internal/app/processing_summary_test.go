package app

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

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
	expected := read(`SELECT session,'',substr(time,1,16),COUNT(*),SUM(activity),SUM(is_error) FROM logical_events WHERE conversation=0 GROUP BY session,substr(time,1,16)
 UNION ALL SELECT session,origin,substr(time,1,16),COUNT(*),SUM(activity),SUM(is_error) FROM record_facts WHERE conversation=0 GROUP BY session,origin,substr(time,1,16)`)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("processing buckets: got %v; want %v", actual, expected)
	}
}
func TestProcessingSummariesRetainExactRangesAndOrigins(t *testing.T) {
	a := setup(t)
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
						query, args := processingCountSQL("codex:buckets", origin, after, before)
						if !cached {
							scope, values := timelineScope("codex:buckets", origin)
							scope += " AND conversation=0"
							if after != nil {
								scope, values = positionBound(scope, values, ">", *after)
							}
							if before != nil {
								scope, values = positionBound(scope, values, "<", *before)
							}
							query = "SELECT COUNT(*),COALESCE(SUM(activity),0),COALESCE(SUM(is_error),0) FROM " + timelineTable(origin) + " WHERE " + scope
							args = values
						}
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
	if _, err := a.DB.Exec("DELETE FROM records WHERE session='codex:buckets'"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	assertProcessingBuckets(t, a)
}
