package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArchiveStatsTrackChangesAndFolderSelection(t *testing.T) {
	a := setup(t)
	old := func(id, folder string) Event {
		e := recorded(t, id, "import", "codex", id, "gen_ai.user.message", map[string]any{"prompt": "old", "cwd": folder, "gen_ai.message.id": id})
		e.Time = "2025-01-01T00:00:00.000000000Z"
		return e
	}
	first, second, other, unknown := old("first", "/a/app"), old("second", "/a/app"), old("other", "/b/app"), old("unknown", "")
	live := recorded(t, "live", "otel", "claude", "live", "user_prompt", map[string]any{"prompt": "live", "cwd": "/a/app"})
	native := recorded(t, "first-copy", "otel", "codex", "first", "gen_ai.user.message", map[string]any{"message_id": "first", "prompt": "old", "cwd": "/a/app"})
	native.Time = first.Time
	save(t, a, first, native, second, other, unknown, live)
	checkStats := func() {
		t.Helper()
		var wrong int
		err := a.DB.QueryRow(`SELECT COUNT(*) FROM (SELECT session,COUNT(*) AS records,SUM(length(raw)) AS raw_bytes FROM records GROUP BY session) e LEFT JOIN record_totals a ON a.session=e.session WHERE a.session IS NULL OR a.records<>e.records OR a.raw_bytes<>e.raw_bytes`).Scan(&wrong)
		if err != nil || wrong != 0 {
			t.Fatal(wrong, err)
		}
	}
	checkStats()
	first = old("first", "/a/app")
	doc, err := decodeDocument(first.Raw)
	if err != nil {
		t.Fatal(err)
	}
	doc.Record["body"] = map[string]any{"stringValue": strings.Repeat("x", 10000)}
	first.Raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	save(t, a, first)
	checkStats()
	var grouped struct {
		Folders       []ArchiveFolder
		TotalFolders  int `json:"total_folders"`
		TotalSessions int `json:"total_sessions"`
	}
	getJSON(t, a, "/api/archive?group=folders", &grouped)
	if grouped.TotalFolders != 3 || grouped.TotalSessions != 4 {
		t.Fatal(grouped)
	}
	var selected ArchiveFolder
	for _, f := range grouped.Folders {
		if f.Path == "/a/app" {
			selected = f
		}
	}
	if selected.Sessions != 2 {
		t.Fatal("active session included", selected)
	}
	w := archiveReq(a, "POST", "/api/archives", `{"cutoff":"2025-02-01T00:00:00Z","folders":["/a/app"]}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job ArchiveJob
	json.Unmarshal(w.Body.Bytes(), &job)
	if job.SessionCount != 2 || job.RecordCount != 3 {
		t.Fatal(job)
	}
	waitArchive(t, a, job.ID, "ready")
	// Folder membership is frozen in the downloadable snapshot.
	getJSON(t, a, "/api/archive?group=folders", &grouped)
	if grouped.TotalFolders != 1 || grouped.Folders[0].Path != "/a/app" || grouped.TotalSessions != 2 {
		t.Fatal(grouped)
	}
	if w = archiveReq(a, "POST", "/api/archives/"+job.ID+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 409 {
		t.Fatal("deletion before download", w.Code)
	}
	archiveReq(a, "GET", "/api/archives/"+job.ID+"/download", "")
	if w = archiveReq(a, "POST", "/api/archives/"+job.ID+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitArchive(t, a, job.ID, "deleted")
	checkStats()
	if countEvents(t, a) != 3 {
		t.Fatal("other folder or active session deleted")
	}
	getJSON(t, a, "/api/archive?group=folders", &grouped)
	if grouped.TotalFolders != 2 || grouped.TotalSessions != 2 {
		t.Fatal(grouped)
	}
}

func TestArchiveFolderSelectionValidation(t *testing.T) {
	a := setup(t)
	for _, body := range []string{`{"folders":[]}`, `{"folders":["a","a"]}`, `{"folders":["a"],"sessions":["s"]}`, `{"folders":["a"],"all":true}`} {
		body = strings.TrimSuffix(body, "}") + `,"cutoff":"2025-02-01T00:00:00Z"}`
		if w := archiveReq(a, "POST", "/api/archives", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if w := archiveReq(a, "POST", "/api/archives", `{"folders":["missing"],"cutoff":"2025-02-01T00:00:00Z"}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestArchiveListUsesMetadataIndex(t *testing.T) {
	a := setup(t)
	rows, err := a.DB.Query("EXPLAIN QUERY PLAN SELECT COUNT(*),SUM(records),SUM(raw_bytes) FROM ("+inactiveSessions+")", "2025-02-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "catalog_archive_candidates") || strings.Contains(plan, "SCAN events") {
		t.Fatal(plan)
	}
}

func TestArchiveEligibilityKeepsUnknownOriginalTimestampsExcluded(t *testing.T) {
	a := setup(t)
	imported := recorded(t, "i", "import", "codex", "unknown-time", "gen_ai.user.message", map[string]any{"gen_ai.message.id": "same", "prompt": "old", "cwd": "/work/app"})
	imported.Time = "1970-01-01T00:00:00.000000000Z"
	native := recorded(t, "n", "otel", "codex", "unknown-time", "gen_ai.user.message", map[string]any{"message_id": "same", "prompt": "old", "cwd": "/work/app"})
	native.Time = "2025-01-01T00:00:00.000000000Z"
	save(t, a, imported, native)
	p := page(t, a, "codex:unknown-time", "", 0)
	if p.Count != 1 {
		t.Fatal("fixture did not pair", p)
	}
	var archive struct {
		TotalSessions int `json:"total_sessions"`
	}
	getJSON(t, a, "/api/archive?group=folders", &archive)
	if archive.TotalSessions != 0 {
		t.Fatal("unknown original timestamp was hidden by a known counterpart", archive)
	}
}

func TestArchiveProgressStreamTerminalAndAuthorization(t *testing.T) {
	a := setup(t)
	if _, err := a.DB.Exec("INSERT INTO archive_jobs(id,status,created_at,cutoff) VALUES('terminal','ready','','')"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	for _, authorized := range []bool{false, true} {
		req, _ := http.NewRequest("GET", server.URL+"/api/archives/terminal/events", nil)
		if authorized {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !authorized {
			if response.StatusCode != 401 {
				t.Fatal(response.StatusCode)
			}
			continue
		}
		if response.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(body), `"status":"ready"`) {
			t.Fatal(response.StatusCode, string(body))
		}
	}
	changes := a.archiveChanges()
	a.notifyArchive()
	select {
	case <-changes:
	default:
		t.Fatal("notification lost")
	}
	if a.archiveChanges() == changes {
		t.Fatal("notification channel not renewed")
	}
}
