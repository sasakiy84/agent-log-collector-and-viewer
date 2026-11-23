package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func directDeleteCandidates(t *testing.T, a *App, query string) []deletionSession {
	t.Helper()
	var payload struct{ Sessions []deletionSession }
	getJSON(t, a, "/api/archive/purge"+query, &payload)
	return payload.Sessions
}

func directDeleteRequest(t *testing.T, a *App, selection []deletionSelection, confirmation bool) ArchiveJob {
	t.Helper()
	body, err := json.Marshal(map[string]any{"sessions": selection, "confirmation": confirmation})
	if err != nil {
		t.Fatal(err)
	}
	response := archiveReq(a, "POST", "/api/archive/purge", string(body))
	if response.Code != 202 {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	var job ArchiveJob
	if err = json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestDirectDeleteSortsByBytesAndFiltersByAge(t *testing.T) {
	a := setup(t)
	oldest := oldEvent(t, "oldest", "oldest")
	large := recorded(t, "large", "import", "codex", "large", "user_prompt", map[string]any{"prompt": strings.Repeat("big ", 300)})
	large.Time = timestamp(time.Now().AddDate(0, 0, -40))
	recent := oldEvent(t, "recent", "recent")
	recent.Time = timestamp(time.Now())
	unknown := oldEvent(t, "unknown", "unknown")
	unknown.Time = knownSessionTime
	save(t, a, oldest, large, recent, unknown)
	if sessions := directDeleteCandidates(t, a, ""); len(sessions) != 4 || sessions[0].ID != large.Session {
		t.Fatal("largest session was not listed first", sessions)
	}
	if sessions := directDeleteCandidates(t, a, "?days=30&sort=oldest"); len(sessions) != 2 || sessions[0].ID != oldest.Session || sessions[1].ID != large.Session {
		t.Fatal("age filter or activity ordering incorrect", sessions)
	}
	if sessions := directDeleteCandidates(t, a, "?days=30&sort=newest"); len(sessions) != 2 || sessions[0].ID != large.Session {
		t.Fatal("newest activity was not listed first", sessions)
	}
}

func TestDirectDeleteRemovesRecordsIndexesAndReclaimsSpace(t *testing.T) {
	a := setup(t)
	removed := recorded(t, "remove", "import", "codex", "remove", "user_prompt", map[string]any{
		"prompt": "deletedmessage", "request_id": "deleted-request-id", "metadata": strings.Repeat("unused bytes ", 200000),
	})
	kept := recorded(t, "keep", "import", "codex", "keep", "user_prompt", map[string]any{"prompt": "survivingmessage"})
	save(t, a, removed, kept)
	if _, err := a.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	before := a.diskBytes()
	list := directDeleteCandidates(t, a, "")
	if len(list) != 2 || list[0].ID != removed.Session || len(searchSessions(t, a, "deletedmessage")) != 1 {
		t.Fatal("missing deletion fixture", list)
	}
	job := directDeleteRequest(t, a, []deletionSelection{{ID: list[0].ID, Revision: list[0].Revision}}, true)
	done := waitArchive(t, a, job.ID, "deleted")
	if !done.DirectDelete || done.DeletedRecords != 1 || done.CompactionError != "" || done.ReclaimedBytes == 0 || a.diskBytes() >= before {
		t.Fatalf("disk space not returned: before=%d after=%d job=%+v", before, a.diskBytes(), done)
	}
	if _, err := os.Stat(a.archiveDir); !os.IsNotExist(err) {
		t.Fatal("direct deletion created an archive directory", err)
	}
	if got := countEvents(t, a); got != 1 {
		t.Fatal("deleted records outside the selection", got)
	}
	if len(searchSessions(t, a, "deletedmessage")) != 0 || len(identifierSessions(t, a, "request_id", "deleted-request-id")) != 0 || len(searchSessions(t, a, "survivingmessage")) != 1 {
		t.Fatal("search did not reflect exactly the selected deletion")
	}
	for _, table := range []string{"sessions", "session_versions", "record_totals", "search_documents", "session_usage", "logical_events", "archive_sessions", "archive_locks"} {
		var count int
		if err := a.DB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE session=?", removed.Session).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %d rows: %v", table, count, err)
		}
	}
	var tombstones int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM record_tombstones").Scan(&tombstones); err != nil || tombstones != 0 {
		t.Fatal("direct deletion retained tombstones", tombstones, err)
	}
}

func TestDirectDeleteRefusesUnconfirmedAndChangedSelection(t *testing.T) {
	a := setup(t)
	old := oldEvent(t, "old", "target")
	save(t, a, old)
	list := directDeleteCandidates(t, a, "")
	input := map[string]any{"sessions": []deletionSelection{{ID: list[0].ID, Revision: list[0].Revision}}}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if response := archiveReq(a, "POST", "/api/archive/purge", string(body)); response.Code != 400 {
		t.Fatal(response.Code, response.Body)
	}
	save(t, a, oldEvent(t, "added", "target"))
	input["confirmation"] = true
	body, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if response := archiveReq(a, "POST", "/api/archive/purge", string(body)); response.Code != 409 {
		t.Fatal(response.Code, response.Body)
	}
	if countEvents(t, a) != 2 {
		t.Fatal("stale confirmation deleted records")
	}
}
