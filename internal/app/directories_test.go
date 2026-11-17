package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

type folderPage struct {
	Folders []FolderSummary `json:"folders"`
	Total   int             `json:"total"`
	Offset  int             `json:"offset"`
}

func TestFoldersGroupWholeSessionsBeforePaginationAndFiltering(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for i := 0; i < 205; i++ {
		e := recorded(t, fmt.Sprint(i), "import", "codex", fmt.Sprint(i), "gen_ai.user.message", map[string]any{"cwd": "/work/shop", "prompt": "認証を調査"})
		records = append(records, e)
	}
	records = append(records,
		recorded(t, "claude", "otel", "claude", "one", "user_prompt", map[string]any{"cwd": "/work/shop", "prompt": "shared"}),
		recorded(t, "other", "otel", "copilot", "two", "user_prompt", map[string]any{"cwd": "/other/shop", "prompt": "shared"}),
		recorded(t, "unknown", "otel", "codex", "unknown", "user_prompt", map[string]any{"prompt": "no directory"}),
	)
	save(t, a, records...)
	var p folderPage
	getJSON(t, a, "/api/folders", &p)
	if p.Total != 3 || len(p.Folders) != 3 {
		t.Fatalf("groups: %+v", p)
	}
	counts := map[string]int{}
	for _, f := range p.Folders {
		counts[f.Path] = f.Count
	}
	if counts["/work/shop"] != 206 || counts["/other/shop"] != 1 || counts[""] != 1 {
		t.Fatal(counts)
	}
	for _, tc := range []struct {
		query string
		count int
	}{
		{"?source=claude", 1},
		{"?q=" + url.QueryEscape("認証"), 205},
	} {
		getJSON(t, a, "/api/folders"+tc.query, &p)
		if p.Total != 1 || p.Folders[0].Path != "/work/shop" || p.Folders[0].Count != tc.count {
			t.Fatal(tc.query, p)
		}
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?folder="+url.QueryEscape("/work/shop")+"&offset=200", &sessions)
	if len(sessions) != 6 {
		t.Fatal("folder was grouped only within a page", len(sessions))
	}
	getJSON(t, a, "/api/sessions?folder=", &sessions)
	if len(sessions) != 1 || sessions[0].ID != "codex:unknown" {
		t.Fatal("unknown folder", sessions)
	}
	getJSON(t, a, "/api/sessions?folder="+url.QueryEscape("/work"), &sessions)
	if len(sessions) != 0 {
		t.Fatal("prefix grouping leaked another directory", sessions)
	}
}

func TestFolderMetadataFallbackAndIncrementalChanges(t *testing.T) {
	a := setup(t)
	first := recorded(t, "first", "import", "codex", "one", "user_prompt", map[string]any{"prompt": "first without metadata"})
	later := recorded(t, "later", "import", "codex", "one", "gen_ai.client.operation", map[string]any{"cwd": "/work/fallback"})
	later.Sequence = 1
	save(t, a, first, later)
	var p folderPage
	getJSON(t, a, "/api/folders", &p)
	if p.Total != 1 || p.Folders[0].Path != "/work/fallback" {
		t.Fatal(p)
	}
	changed := recorded(t, "first", "import", "codex", "one", "user_prompt", map[string]any{"cwd": "/work/preferred", "prompt": "first with metadata"})
	save(t, a, changed)
	getJSON(t, a, "/api/folders", &p)
	if p.Total != 1 || p.Folders[0].Path != "/work/preferred" {
		t.Fatal(p)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", changed.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	getJSON(t, a, "/api/folders", &p)
	if p.Folders[0].Path != "/work/fallback" {
		t.Fatal(p)
	}
	if _, err := a.DB.Exec("DELETE FROM records"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	getJSON(t, a, "/api/folders?offset=9999", &p)
	if p.Total != 0 || p.Offset != 0 || len(p.Folders) != 0 {
		t.Fatal("empty folder survived deletion", p)
	}
}

func TestFolderPagesAndActivityOrdering(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for i := 0; i < 205; i++ {
		e := recorded(t, fmt.Sprint(i), "import", "codex", fmt.Sprint(i), "user_prompt", map[string]any{"cwd": fmt.Sprintf("/work/%03d", i)})
		records = append(records, e)
	}
	records[204].Time = "2026-11-15T00:00:00.000000000Z"
	save(t, a, records...)
	var p folderPage
	getJSON(t, a, "/api/folders", &p)
	if p.Total != 205 || len(p.Folders) != 200 || p.Folders[0].Path != "/work/204" {
		t.Fatal(p.Total, len(p.Folders), p.Folders[0])
	}
	getJSON(t, a, "/api/folders?offset=999999", &p)
	if p.Offset != 200 || len(p.Folders) != 5 {
		t.Fatal(p)
	}
}

func TestDirectoryMetadataCannotUnboundNormalPages(t *testing.T) {
	a := setup(t)
	for i, value := range []any{strings.Repeat("x", 1<<20), map[string]any{"huge": strings.Repeat("y", 1<<20)}} {
		e := recorded(t, fmt.Sprint(i), "import", "codex", "oversize", "user_prompt", map[string]any{"cwd": value, "prompt": "bounded metadata"})
		if _, ok := value.(map[string]any); ok {
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range arr(doc.Record["attributes"]) {
				attr := obj(item)
				if attr["key"] == "cwd" {
					attr["value"] = map[string]any{"kvlistValue": map[string]any{"values": []any{map[string]any{"key": "huge", "value": map[string]any{"stringValue": value.(map[string]any)["huge"]}}}}}
				}
			}
			if e.Raw, err = json.Marshal(doc); err != nil {
				t.Fatal(err)
			}
		}
		save(t, a, e)
	}
	w := archiveReq(a, "GET", "/api/session?id=codex:oversize", "")
	if w.Code != 200 || w.Body.Len() > 20000 {
		t.Fatal("directory escaped presentation limit", w.Code, w.Body.Len())
	}
	var p folderPage
	getJSON(t, a, "/api/folders", &p)
	if p.Total != 1 || p.Folders[0].Path != "" {
		t.Fatal(p)
	}
}
