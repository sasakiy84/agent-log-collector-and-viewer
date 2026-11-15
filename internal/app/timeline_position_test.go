package app

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

func TestSessionsSortByNewestActivity(t *testing.T) {
	a := setup(t)
	old := recorded(t, "old", "import", "codex", "old", "gen_ai.user.message", map[string]any{"prompt": "shared ordering fixture"})
	old.Time = timestamp(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := recorded(t, "new", "import", "codex", "new", "gen_ai.user.message", map[string]any{"prompt": "shared ordering fixture"})
	newer.Time = timestamp(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	save(t, a, newer, old)
	rows := searchSessions(t, a, "")
	if len(rows) != 2 || rows[0].ID != newer.Session {
		t.Fatal("insertion order used instead of latest activity", rows)
	}
	update := recorded(t, "update", "otel", "codex", "old", "gen_ai.user.message", map[string]any{"prompt": "shared ordering fixture"})
	update.Time = timestamp(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	save(t, a, update)
	for _, path := range []string{"/api/sessions", "/api/sessions?source=codex", "/api/sessions?q=shared"} {
		var rows []SessionSummary
		getJSON(t, a, path, &rows)
		if len(rows) != 2 || rows[0].ID != old.Session || rows[1].ID != newer.Session {
			t.Fatal(path, rows)
		}
	}
}
func TestLatestTimelineWindowIsChronologicalAndOriginIsolated(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for n := 0; n < 405; n++ {
		for _, origin := range []string{"import", "otel"} {
			event := recorded(t, fmt.Sprintf("%s-%d", origin, n), origin, "codex", "latest", "gen_ai.user.message", map[string]any{"event.id": fmt.Sprint(n), "prompt": fmt.Sprint("event ", n)})
			event.Time = timestamp(time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC))
			event.Sequence = n
			records = append(records, event)
		}
	}
	save(t, a, records...)
	for _, origin := range []string{"", "import", "otel"} {
		var p SessionPage
		getJSON(t, a, "/api/session?id=codex:latest&position=latest&origin="+origin, &p)
		if p.Count != 405 || p.Offset != 400 || p.Limit != 200 || len(p.Events) != 5 {
			t.Fatalf("%s: count=%d offset=%d limit=%d rows=%d", origin, p.Count, p.Offset, p.Limit, len(p.Events))
		}
		for i, event := range p.Events {
			want := fmt.Sprintf("import:import-%d", 400+i)
			wantRecords := 2
			if origin == "import" {
				wantRecords = 1
			}
			if origin == "otel" {
				want = fmt.Sprintf("otel-%d", 400+i)
				wantRecords = 1
			}
			if event.ID != want || len(event.Records) != wantRecords {
				t.Fatalf("%s/%d: %s %d", origin, i, event.ID, len(event.Records))
			}
		}
	}
	var small SessionPage
	getJSON(t, a, "/api/session?id=codex:latest&position=latest&limit=5", &small)
	if small.Offset != 400 || len(small.Events) != 5 || small.Events[4].ID != "import:import-404" {
		t.Fatal(small.Offset, len(small.Events))
	}
	// Historical requests retain their explicit window and look-ahead record.
	var historical SessionPage
	getJSON(t, a, "/api/session?id=codex:latest&offset=0&limit=5", &historical)
	if historical.Offset != 0 || historical.Limit != 5 || len(historical.Events) != 6 || historical.Events[0].ID != "import:import-0" {
		t.Fatal(historical)
	}
}
func TestLatestShortAndEmptyOrigin(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "only", "otel", "claude", "short", "user_prompt", map[string]any{"prompt": "one event"}))
	for _, origin := range []string{"", "otel", "import"} {
		var p SessionPage
		getJSON(t, a, "/api/session?id=claude:short&position=latest&origin="+origin, &p)
		want := 1
		if origin == "import" {
			want = 0
		}
		if p.Offset != 0 || p.Count != want || len(p.Events) != want {
			t.Fatal(origin, p)
		}
	}
	for _, query := range []string{"limit=0", "limit=201", "limit=x", "position=invalid"} {
		w := archiveReq(a, "GET", "/api/session?id="+url.QueryEscape("claude:short")+"&"+query, "")
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
}
