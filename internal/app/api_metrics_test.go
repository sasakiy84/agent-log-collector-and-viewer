package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestServerMetricsUseOriginalAndLogicalCounters(t *testing.T) {
	a := setup(t)
	a.Version = "v-metrics-test"
	key := newTestAPIKey(t, a)
	imported := recorded(t, "imported", "import", "codex", "root", "gen_ai.user.message", map[string]any{"event.id": "shared", "prompt": "hello"})
	native := recorded(t, "native", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"event.id": "shared", "prompt": "hello"})
	other := recorded(t, "other", "otel", "claude", "other", "gen_ai.user.message", map[string]any{"prompt": "hello"})
	save(t, a, imported, native, other)
	var metrics serverMetrics
	fetch := func() {
		t.Helper()
		w := keyRequest(a, "GET", "/api/metrics", "", key.Key)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &metrics); err != nil {
			t.Fatal(err)
		}
	}
	fetch()
	if metrics.Version != "v-metrics-test" || !metrics.CountsReady || *metrics.Events != 3 || *metrics.LogicalEvents != 2 || *metrics.Sessions != 2 || *metrics.RawBytes != int64(len(imported.Raw)+len(native.Raw)+len(other.Raw)) {
		t.Fatal(metrics)
	}
	if _, err := time.Parse(time.RFC3339Nano, metrics.StartedAt); err != nil || metrics.UptimeSeconds < 0 {
		t.Fatal(metrics.StartedAt, err)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", other.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	fetch()
	if *metrics.Events != 2 || *metrics.Sessions != 1 || *metrics.LogicalEvents != 1 {
		t.Fatal(metrics)
	}
	// Partial historical metadata is unknown, never a misleading zero or a raw scan.
	if _, err := a.DB.Exec("INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('storage','legacy','')"); err != nil {
		t.Fatal(err)
	}
	a.maintained = make(chan struct{})
	fetch()
	if metrics.CountsReady || metrics.Events != nil || metrics.Sessions != nil || metrics.RawBytes != nil || metrics.LogicalEvents != nil || metrics.State != "preparing" {
		t.Fatal(metrics)
	}
	w := keyRequest(a, "GET", "/api/readme", "", key.Key)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") || w.Body.String() != apiReference || !strings.Contains(w.Body.String(), "/api/metrics") {
		t.Fatal("API README differs from embedded reference")
	}
}
