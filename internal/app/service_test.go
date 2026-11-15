package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func serviceSettled(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if closed(a.maintained) {
			if a.maintenanceErr != nil {
				t.Fatal(a.maintenanceErr)
			}
			var pending int
			if err := a.inbox.QueryRow("SELECT COUNT(*) FROM ingest_batches").Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if pending == 0 {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("service did not finish preparation/inbox processing")
}

func TestDurableReceiverDuringMigrationAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "service.db")
	original := logBody("codex", "original", "", "")
	a, err := Open(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	if w := request(a, "/v1/logs", original, token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a.Close()
	lockDB := migrationDB(t, path)
	lock, err := lockDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	lifetime, cancel := context.WithCancel(ctx)
	a, err = Start(lifetime, path, token)
	if err != nil {
		t.Fatal(err)
	}
	// The main writer/migration is blocked. Neither ACK waits for that database.
	for _, body := range []string{original, logBody("codex", "queued", "", "")} {
		w := request(a, "/v1/logs", body, token)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var pending int
	if err = a.inbox.QueryRow("SELECT COUNT(*) FROM ingest_batches").Scan(&pending); err != nil || pending != 2 {
		t.Fatal(pending, err)
	}
	w := archiveReq(a, "GET", "/", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	// Cancel before releasing the main lock: queued ACKs must survive a restart.
	cancel()
	lock.Rollback()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Start(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	serviceSettled(t, a)
	if countEvents(t, a) != 2 {
		t.Fatal("lost queued data or duplicated committed data", countEvents(t, a))
	}
	var p SessionPage
	getJSON(t, a, "/api/session?id=codex:queued", &p)
	if p.Count != 1 {
		t.Fatal(p)
	}
}

func TestReceiverAndReadersDoNotWaitForViewWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.db")
	a, err := Start(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	serviceSettled(t, a)
	if w := request(a, "/v1/logs", logBody("codex", "existing", "", ""), token); w.Code != 200 {
		t.Fatal(w.Code)
	}
	serviceSettled(t, a)
	conn, err := a.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w := request(a, "/v1/logs", logBody("codex", "later", "", ""), token)
		if w.Code != 200 {
			t.Errorf("queued receiver: %d", w.Code)
		}
		r := httptest.NewRequest("GET", "/api/sessions", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, r)
		var sessions []SessionSummary
		if err := json.Unmarshal(response.Body.Bytes(), &sessions); err != nil || response.Code != 200 || len(sessions) != 1 {
			t.Errorf("existing views unavailable: %d %s", response.Code, response.Body.String())
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		conn.Close()
		<-done
		t.Fatal("HTTP blocked on the view writer")
	}
	conn.Close()
	serviceSettled(t, a)
	if countEvents(t, a) != 2 {
		t.Fatal("pending batch was not processed")
	}
}

func TestQueueCapacityRejectsWithoutAcknowledgingOrDropping(t *testing.T) {
	a, err := Start(context.Background(), filepath.Join(t.TempDir(), "service.db"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	serviceSettled(t, a)
	if _, err = a.inbox.Exec("UPDATE ingest_size SET bytes=?", maxInboxBytes); err != nil {
		t.Fatal(err)
	}
	w := request(a, "/v1/logs", logBody("codex", "full", "", ""), token)
	if w.Code != 503 {
		t.Fatal("acknowledged a full queue", w.Code)
	}
	var pending int
	if err = a.inbox.QueryRow("SELECT COUNT(*) FROM ingest_batches").Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
	if _, err = a.inbox.Exec("UPDATE ingest_size SET bytes=0"); err != nil {
		t.Fatal(err)
	}
	if countEvents(t, a) != 0 {
		t.Fatal("rejected batch became visible")
	}
}

func TestIncompleteMaintenanceKeepsViewsAndQueuedData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "service.db")
	a, err := Open(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	if w := request(a, "/v1/logs", logBody("codex", "existing", "", ""), token); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// Simulate a failing historical batch while the previously committed view
	// is still valid. The checkpoint must remain resumable after correction.
	if _, err = a.DB.Exec(`UPDATE maintenance_jobs SET cursor='',processed=0,complete=0 WHERE name='usage.v1';
 CREATE TRIGGER fixture_maintenance_failure BEFORE UPDATE ON maintenance_jobs
 WHEN NEW.name='usage.v1' BEGIN SELECT RAISE(ABORT,'fixture maintenance failure');END;`); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = Start(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.maintained:
	case <-time.After(5 * time.Second):
		a.Close()
		t.Fatal("maintenance did not reach the failing batch")
	}
	if a.startupErr != nil || a.maintenanceErr == nil {
		t.Fatal("failure was not isolated to historical preparation", a.startupErr, a.maintenanceErr)
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].ID != "codex:existing" {
		t.Fatal("existing views disappeared", sessions)
	}
	if w := request(a, "/v1/logs", logBody("codex", "retained", "", ""), token); w.Code != 200 {
		t.Fatal("maintenance failure disabled reception", w.Code)
	}
	if _, err = a.DB.Exec("DROP TRIGGER fixture_maintenance_failure"); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = Start(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	serviceSettled(t, a)
	if countEvents(t, a) != 2 {
		t.Fatal("queued ACK was lost during failed maintenance")
	}
}

func TestBulkImportsReserveSpaceForLiveOTLP(t *testing.T) {
	a, err := newApp(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.openInbox(filepath.Join(t.TempDir(), "views.db")); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.inbox.Exec("UPDATE ingest_size SET bytes=?", maxImportBytes); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = decodeJSON([]byte(logBody("codex", "import", "", "")), &root); err != nil {
		t.Fatal(err)
	}
	resource := obj(obj(arr(root["resourceLogs"])[0])["resource"])
	resource["attributes"] = append(arr(resource["attributes"]), map[string]any{"key": "agentlog.origin", "value": map[string]any{"stringValue": "import"}})
	body, _ := json.Marshal(root)
	w := request(a, "/v1/logs", string(body), token)
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("bulk import used the live reserve", w.Code, w.Header())
	}
	w = request(a, "/v1/logs", logBody("codex", "live", "", ""), token)
	if w.Code != 200 {
		t.Fatal("live OTLP was rejected by the bulk threshold", w.Code)
	}
	var batches int
	if err = a.inbox.QueryRow("SELECT batches FROM ingest_size WHERE id=1").Scan(&batches); err != nil || batches != 1 {
		t.Fatal("incorrect queue accounting", batches, err)
	}
}
