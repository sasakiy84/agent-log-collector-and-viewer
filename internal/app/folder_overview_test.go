package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestFolderOverviewMatchesFilteredAccountingAfterChanges(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	check := func() {
		t.Helper()
		for _, query := range []string{"", "?source=codex", "?diagnostics=1", "?source=codex&diagnostics=1", "?folder=" + url.QueryEscape("/work/shared"), "?project=path:/work/shared", "?project=path:/work/shared&source=claude", "?project=", "?project=name:missing"} {
			var cached, original folderPage
			getJSON(t, a, "/api/folders"+query, &cached)
			// A durable pending marker selects the original full-session query.
			if _, err := a.DB.Exec("INSERT INTO folder_dirty VALUES('external-edit')"); err != nil {
				t.Fatal(err)
			}
			getJSON(t, a, "/api/folders"+query, &original)
			if !reflect.DeepEqual(cached, original) {
				t.Fatalf("cached overview differs for %s:\ncached=%+v\noriginal=%+v", query, cached, original)
			}
			if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error { return refreshFolders(ctx, tx) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	unknown := recorded(t, "unknown", "otel", "claude", "unknown", "user_prompt", map[string]any{"cwd": "/work/shared", "prompt": "unknown usage"})
	counter := func(folder, tokens, cost string) Event {
		return recorded(t, "usage", "import", "codex", "measured", "api_request", map[string]any{"cwd": folder, "input_tokens": tokens, "output_tokens": "0", "cost_usd": cost})
	}
	save(t, a, unknown, counter("/work/shared", "9007199254740993", "0.000000000000000001"))
	save(t, a,
		recorded(t, "overlap-import", "import", "codex", "overlap", "api_request", map[string]any{"cwd": "/work/shared", "input_tokens": "12"}),
		recorded(t, "overlap-otel", "otel", "codex", "overlap", "api_request", map[string]any{"cwd": "/work/shared", "input_tokens": "13"}),
	)
	diagnostic := recorded(t, "diagnostic", "otel", "codex", "unused", "api_request", map[string]any{"cwd": "/work/shared", "input_tokens": "7"})
	diagnostic.Session = "trace:codex:synthetic"
	save(t, a, diagnostic)
	save(t, a, recorded(t, "automatic", "otel", "codex", "automatic", "thread/start", map[string]any{"cwd": "/work/shared", "session_init.ephemeral": true, "rpc.request_id": "temporary-structured-test"}))
	check()
	// A changed import moves its contribution and replaces exact usage values.
	save(t, a, counter("/work/moved", "0", "0.123456789012345678"))
	check()
	save(t, a, counter("/work/shared", "9007199254740994", "0"))
	check()
	// Exercise the same lock transitions as confirmed archive deletion.
	if _, err := a.DB.Exec("INSERT INTO archives(id,status,created_at,cutoff,session_count,record_count,raw_bytes) VALUES('folder-test','preparing','','',1,1,1)"); err != nil {
		t.Fatal(err)
	}
	for _, lock := range []bool{true, false} {
		if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
			query := "DELETE FROM archive_locks WHERE session='codex:measured'"
			if lock {
				query = "INSERT INTO archive_locks VALUES('codex:measured','folder-test')"
			}
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return err
			}
			return refreshFolders(ctx, tx)
		}); err != nil {
			t.Fatal(err)
		}
		check()
	}
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id IN('import:overlap-import','overlap-otel')"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	check()
	// Removing the last measured member leaves unknown usage, not invented zeroes.
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id='import:usage'"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestCachedFolderPagesMatchDynamicPages(t *testing.T) {
	a := setup(t)
	records := []Event{}
	for i := 0; i < 211; i++ {
		source := "codex"
		if i%2 == 0 {
			source = "claude"
		}
		records = append(records, recorded(t, fmt.Sprint(i), "otel", source, fmt.Sprint(i), "user_prompt", map[string]any{"cwd": fmt.Sprintf("/work/folder-%03d", i), "prompt": "synthetic"}))
	}
	save(t, a, records...)
	for _, query := range []string{"?offset=200", "?offset=999999", "?source=claude&offset=200"} {
		var cached, dynamic folderPage
		getJSON(t, a, "/api/folders"+query, &cached)
		if _, err := a.DB.Exec("INSERT INTO folder_dirty VALUES('external-edit')"); err != nil {
			t.Fatal(err)
		}
		getJSON(t, a, "/api/folders"+query, &dynamic)
		if !reflect.DeepEqual(cached, dynamic) {
			t.Fatalf("folder page changed: %s", query)
		}
		if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error { return refreshFolders(context.Background(), tx) }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFolderProjectionRollbackAndLatestEndpoint(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	save(t, a,
		recorded(t, "first", "otel", "codex", "first", "user_prompt", map[string]any{"cwd": "/work/same", "prompt": "first"}),
		recorded(t, "last", "otel", "codex", "last", "user_prompt", map[string]any{"cwd": "/work/same", "prompt": "last"}),
	)
	var before, after folderPage
	getJSON(t, a, "/api/folders", &before)
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE session_catalog SET end='2099-01-01T00:00:00Z' WHERE session='codex:last'"); err == nil {
		err = refreshFolders(ctx, tx)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Rollback()
	getJSON(t, a, "/api/folders", &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rolled-back contribution became visible")
	}
	if err = inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE session_catalog SET end='2099-01-01T00:00:00Z' WHERE session='codex:last'"); err != nil {
			return err
		}
		return refreshFolders(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	getJSON(t, a, "/api/folders", &after)
	if after.Folders[0].End != "2099-01-01T00:00:00Z" {
		t.Fatal("latest member not reflected")
	}
	if err = inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM events WHERE id='last'"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	getJSON(t, a, "/api/folders", &after)
	if after.Folders[0].End != before.Folders[0].End || after.Folders[0].Count != 1 {
		t.Fatal("removing the newest member did not restore the indexed endpoint")
	}
}
