package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type exportManifest struct {
	Format            string `json:"format"`
	Sessions, Records int64
	Internal          bool `json:"include_internal"`
	Filters           map[string]string
}

func decodeExport(t *testing.T, body []byte) (exportManifest, []Event) {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest exportManifest
	events := []Event{}
	for _, file := range z.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(reader)
		if file.Name == "manifest.json" {
			err = decoder.Decode(&manifest)
		} else if file.Name == "events.jsonl" {
			for {
				var event Event
				err = decoder.Decode(&event)
				if err == io.EOF {
					err = nil
					break
				}
				if err != nil {
					break
				}
				events = append(events, event)
			}
		} else {
			t.Fatal("unexpected ZIP member", file.Name)
		}
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if manifest.Format != "agentlog.session-export" || manifest.Records != int64(len(events)) {
		t.Fatal("manifest does not describe exported events", manifest, len(events))
	}
	return manifest, events
}

func TestProjectExportIncludesWholeSessionsAndHiddenData(t *testing.T) {
	a := setup(t)
	root := recorded(t, "root", "import", "codex", "root", "gen_ai.user.message", map[string]any{"prompt": "認証を修正", "service.namespace": "project", "message_id": "shared"})
	native := recorded(t, "native", "otel", "codex", "root", "codex.user_prompt", map[string]any{"prompt": "認証を修正", "service.namespace": "project", "message_id": "shared"})
	reply := recorded(t, "reply", "otel", "codex", "root", "gen_ai.assistant.message", map[string]any{"response": strings.Repeat("context ", 1000) + "full export tail"})
	child := recorded(t, "child", "otel", "codex", "child", "user_prompt", map[string]any{"prompt": "review", "service.namespace": "project"})
	child.Parent = root.Session
	auto := recorded(t, "auto", "otel", "codex", "auto", "codex.user_prompt", map[string]any{"prompt": automaticSuggestion, "service.namespace": "project"})
	claude := recorded(t, "claude", "otel", "claude", "claude", "user_prompt", map[string]any{"prompt": "表示を修正", "service.namespace": "project"})
	other := recorded(t, "other", "otel", "codex", "other", "user_prompt", map[string]any{"prompt": "認証を修正", "service.namespace": "elsewhere"})
	save(t, a, root, native, reply, child, auto, claude, other)
	base := url.Values{"project": {"name:project"}}
	preview := archiveReq(a, "GET", "/api/export/sessions?"+base.Encode(), "")
	if preview.Code != 200 || preview.Header().Get("X-Total-Count") != "4" || preview.Header().Get("X-Record-Count") != "6" {
		t.Fatal("export preview did not include all project data", preview.Code, preview.Header(), preview.Body)
	}
	for _, test := range []struct {
		params            url.Values
		sessions, records int64
	}{
		{base, 4, 6},
		{url.Values{"project": {"name:project"}, "q": {"認証"}}, 1, 3},
		{url.Values{"project": {"name:project"}, "id_key": {"event.id"}, "id_value": {"reply"}}, 1, 3},
		{url.Values{"project": {"name:project"}, "source": {"claude"}}, 1, 1},
		{url.Values{"project": {"name:project"}, "diagnostics": {"0"}}, 3, 5},
	} {
		response := archiveReq(a, "GET", "/api/export/download?"+test.params.Encode(), "")
		if response.Code != 200 || response.Header().Get("Content-Type") != "application/zip" {
			t.Fatal(response.Code, response.Body)
		}
		manifest, events := decodeExport(t, response.Body.Bytes())
		if manifest.Sessions != test.sessions || manifest.Records != test.records {
			t.Fatal(test.params, manifest)
		}
		for _, event := range events {
			if event.Session == other.Session {
				t.Fatal("another project leaked into export")
			}
		}
	}
	// Export is read-only; archive locks must not silently omit still-stored data.
	if _, err := a.DB.Exec("INSERT INTO archives(id,status,created_at,cutoff) VALUES('export-test','ready','','');INSERT INTO archive_locks VALUES('codex:root','export-test')"); err != nil {
		t.Fatal(err)
	}
	response := archiveReq(a, "GET", "/api/export/download?"+base.Encode(), "")
	manifest, events := decodeExport(t, response.Body.Bytes())
	if manifest.Records != 6 {
		t.Fatal("locked project data was omitted", manifest)
	}
	var exportedRoot Event
	for _, event := range events {
		if event.ID == reply.ID && !strings.Contains(string(event.Raw), "full export tail") {
			t.Fatal("export used a truncated presentation")
		}
		if event.ID == root.ID {
			exportedRoot = event
		}
	}
	want, _ := decodeDocument(root.Raw)
	got, _ := decodeDocument(exportedRoot.Raw)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("original OTLP document changed")
	}
}

func TestExportIgnoresPaginationAndSupportsUnassignedProject(t *testing.T) {
	a := setup(t)
	events := []Event{}
	for i := 0; i < 205; i++ {
		events = append(events, recorded(t, fmt.Sprint(i), "otel", "codex", fmt.Sprint(i), "user_prompt", map[string]any{"prompt": "hello"}))
	}
	events = append(events, recorded(t, "other", "otel", "claude", "other", "user_prompt", map[string]any{"prompt": "hello", "service.namespace": "other"}))
	save(t, a, events...)
	preview := archiveReq(a, "GET", "/api/export/sessions?project=&offset=200", "")
	var page []SessionSummary
	if err := json.Unmarshal(preview.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 5 || preview.Header().Get("X-Total-Count") != "205" {
		t.Fatal(preview.Header(), len(page))
	}
	response := archiveReq(a, "GET", "/api/export/download?project=&offset=200", "")
	manifest, _ := decodeExport(t, response.Body.Bytes())
	if manifest.Sessions != 205 || manifest.Records != 205 {
		t.Fatal("only the current page was exported", manifest)
	}
	if _, exists := manifest.Filters["offset"]; exists {
		t.Fatal("pagination became an export filter")
	}
	if _, exists := manifest.Filters["project"]; !exists {
		t.Fatal("empty project filter was lost")
	}
	for path, want := range map[string]int{"/api/export/download?project=name:missing": 404, "/api/export/download?id_key=prompt&id_value=x": 400} {
		if response := archiveReq(a, "GET", path, ""); response.Code != want {
			t.Fatal(path, response.Code)
		}
	}
	request := httptest.NewRequest("GET", "/api/export/download", nil)
	unauthorized := httptest.NewRecorder()
	a.Handler().ServeHTTP(unauthorized, request)
	if unauthorized.Code != 401 {
		t.Fatal("export is unauthenticated", unauthorized.Code)
	}
}

type exportHookWriter struct {
	*httptest.ResponseRecorder
	hook func()
}

func (w *exportHookWriter) Write(p []byte) (int, error) {
	if w.hook != nil {
		hook := w.hook
		w.hook = nil
		hook()
	}
	return w.ResponseRecorder.Write(p)
}

func TestExportUsesOneSnapshotWhileIngestionContinues(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "first", "otel", "codex", "live", "user_prompt", map[string]any{"prompt": "hello"}))
	writer := &exportHookWriter{ResponseRecorder: httptest.NewRecorder(), hook: func() {
		if err := a.Save(context.Background(), []Event{recorded(t, "late", "otel", "codex", "live", "gen_ai.assistant.message", map[string]any{"response": "new arrival"})}); err != nil {
			t.Fatal("export blocked ingestion", err)
		}
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/export/download", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	a.Handler().ServeHTTP(writer, r)
	manifest, events := decodeExport(t, writer.Body.Bytes())
	if manifest.Records != 1 || events[0].ID != "first" {
		t.Fatal("concurrent arrival leaked into export snapshot", manifest, events)
	}
	var count int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil || count != 2 {
		t.Fatal("arrival was lost", count, err)
	}
}

func TestCancelledExportIsNotFinalizedAndReleasesReader(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "one", "otel", "codex", "one", "user_prompt", map[string]any{"prompt": "hello"}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &exportHookWriter{ResponseRecorder: httptest.NewRecorder(), hook: cancel}
	r := httptest.NewRequest("GET", "/api/export/download", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	var aborted any
	func() { defer func() { aborted = recover() }(); a.Handler().ServeHTTP(writer, r) }()
	if aborted != http.ErrAbortHandler {
		t.Fatal("cancelled stream was reported as successful", aborted)
	}
	if _, err := zip.NewReader(bytes.NewReader(writer.Body.Bytes()), int64(writer.Body.Len())); err == nil {
		t.Fatal("cancelled export became a valid partial ZIP")
	}
	if inUse := a.readers.Stats().InUse; inUse != 0 {
		t.Fatal("cancelled export leaked its reader", inUse)
	}
}
