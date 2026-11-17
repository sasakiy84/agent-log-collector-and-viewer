package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVersionDoesNotWaitForDatabaseInitialization(t *testing.T) {
	a := &App{Version: "v-version-test", token: token, initialized: make(chan struct{})}
	for _, failed := range []bool{false, true} {
		if failed {
			a.startupErr = errors.New("database unavailable")
			close(a.initialized)
		}
		w := keyRequest(a, "GET", "/api/version", "", token)
		var data map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 || data["version"] != a.Version {
			t.Fatal("version unavailable without a database", w.Code, w.Body.String(), err)
		}
		if w := keyRequest(a, "GET", "/api/version", "", ""); w.Code != 401 {
			t.Fatal("version bypassed authentication", w.Code)
		}
	}
}

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
	var build map[string]string
	getJSON(t, a, "/api/version", &build)
	if build["version"] != metrics.Version {
		t.Fatal("version differs across APIs", build, metrics.Version)
	}
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
	a.maintained = make(chan struct{})
	fetch()
	if !metrics.CountsReady || *metrics.Events != 2 || *metrics.Sessions != 1 || metrics.LogicalEvents != nil || metrics.State != "preparing" {
		t.Fatal(metrics)
	}
	w := keyRequest(a, "GET", "/api/readme", "", key.Key)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") || w.Body.String() != apiReference || !strings.Contains(w.Body.String(), "/api/metrics") {
		t.Fatal("API README differs from embedded reference")
	}
}
