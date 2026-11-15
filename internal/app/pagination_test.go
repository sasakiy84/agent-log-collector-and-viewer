package app

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSessionPageTotalsAndStaleOffsets(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for i := 0; i < 405; i++ {
		e := oldEvent(t, fmt.Sprint(i), fmt.Sprintf("fixture-%03d", i))
		records = append(records, e)
	}
	records = append(records, recorded(t, "other", "otel", "claude", "other", "user_prompt", map[string]any{"prompt": "different"}))
	save(t, a, records...)
	for _, tc := range []struct {
		query, total, offset string
		rows                 int
	}{
		{"", "406", "0", 201},
		{"?offset=200", "406", "200", 201},
		{"?offset=400", "406", "400", 6},
		{"?offset=999999", "406", "400", 6},
		{"?offset=-5", "406", "0", 201},
		{"?source=claude&offset=200", "1", "0", 1},
		{"?q=synthetic&offset=400", "405", "400", 5},
		{"?source=claude&q=synthetic&offset=400", "0", "0", 0},
	} {
		w := archiveReq(a, "GET", "/api/sessions"+tc.query, "")
		var rows []SessionSummary
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || w.Header().Get("X-Total-Count") != tc.total || w.Header().Get("X-Page-Offset") != tc.offset || len(rows) != tc.rows {
			t.Fatalf("%s: status=%d total=%s offset=%s rows=%d", tc.query, w.Code, w.Header().Get("X-Total-Count"), w.Header().Get("X-Page-Offset"), len(rows))
		}
	}
	var archive struct {
		Total    int              `json:"total_sessions"`
		Offset   int              `json:"offset"`
		Sessions []ArchiveSession `json:"sessions"`
	}
	getJSON(t, a, "/api/archive?offset=999999", &archive)
	if archive.Offset != 400 || archive.Total != 405 || len(archive.Sessions) != 5 {
		t.Fatalf("archive: %+v", archive)
	}
}

func TestTimelineNumberedPagesCoverHistory(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for i := 0; i < 405; i++ {
		e := oldEvent(t, fmt.Sprint(i), "numbered")
		e.Sequence = i
		records = append(records, e)
	}
	save(t, a, records...)
	seen := map[string]bool{}
	for _, offset := range []int{0, 200, 400} {
		p := page(t, a, "codex:numbered", "", offset)
		for _, e := range p.Events[:min(p.Limit, len(p.Events))] {
			if seen[e.ID] {
				t.Fatal("overlapping page", e.ID)
			}
			seen[e.ID] = true
		}
	}
	if len(seen) != 405 {
		t.Fatal("missing events", len(seen))
	}
	p := page(t, a, "codex:numbered", "", 999999)
	if p.Offset != 400 || len(p.Events) != 5 {
		t.Fatal(p.Offset, len(p.Events))
	}
	// Appending changes the last page, never historical page boundaries.
	e := oldEvent(t, "appended", "numbered")
	e.Sequence = 999
	save(t, a, e)
	var latest SessionPage
	getJSON(t, a, "/api/session?id=codex:numbered&position=latest", &latest)
	if latest.Offset != 400 || len(latest.Events) != 6 || latest.Events[5].ID != "import:appended" {
		t.Fatal(latest.Offset, len(latest.Events))
	}
}
