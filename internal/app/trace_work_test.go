package app

import (
	"context"
	"database/sql"
	"testing"
)

func ambiguousTraceFixture(t *testing.T, a *App) {
	t.Helper()
	one := recorded(t, "owner-a", "otel", "codex", "a", "user_prompt", map[string]any{"prompt": "A"})
	one.Trace, one.Span = "shared", "pa"
	two := recorded(t, "owner-b", "otel", "codex", "b", "user_prompt", map[string]any{"prompt": "B"})
	two.Trace, two.Span = "shared", "pb"
	pending := recorded(t, "pending", "otel", "codex", "unused", "fs.read_file", map[string]any{})
	pending.Trace, pending.Session, pending.ParentSpan = "shared", "trace:codex:shared", "unknown"
	save(t, a, one, two, pending)
}

func TestTraceMaintenanceDefersLockedIdentity(t *testing.T) {
	a := setup(t)
	ambiguousTraceFixture(t, a)
	if _, err := a.DB.Exec(`INSERT INTO archives(id,status,created_at,cutoff) VALUES('locked','ready','2025','2026');
 INSERT INTO archive_locks(session,archive_id) VALUES('codex:a','locked'),('codex:b','locked'),('trace:codex:shared','locked');
 DELETE FROM events WHERE id='owner-a'`); err != nil {
		t.Fatal(err)
	}
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		count, err := reconcileTraceBatch(context.Background(), tx)
		if count != 0 {
			t.Fatal("locked identity processed", count)
		}
		return err
	}); err != nil {
		t.Fatal("startup blocked before archive recovery", err)
	}
	var count int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM trace_dirty").Scan(&count); err != nil || count != 1 {
		t.Fatal("deferred work lost", count, err)
	}
	if err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM archive_locks WHERE archive_id='locked'"); err != nil {
			return err
		}
		return refreshProjections(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
	var session string
	if err := a.DB.QueryRow("SELECT session FROM events WHERE id='pending'").Scan(&session); err != nil || session != "codex:b" {
		t.Fatal("unlocked identity did not resume", session, err)
	}
}

func TestStartupTraceMoveMaintainsViews(t *testing.T) {
	a := setup(t)
	ambiguousTraceFixture(t, a)
	if _, err := a.DB.Exec("DELETE FROM events WHERE id='owner-a'"); err != nil {
		t.Fatal(err)
	}
	path := a.dbPath
	a.Close()
	b, err := Open(path, token)
	if err != nil {
		t.Fatal("destination before old session must not collide with old view IDs", err)
	}
	defer b.Close()
	if p := page(t, b, "codex:b", "", 0); p.Count != 2 {
		t.Fatal("moved event missing", p.Count)
	}
	var dirty int
	if err := b.DB.QueryRow("SELECT (SELECT COUNT(*) FROM trace_dirty)+(SELECT COUNT(*) FROM read_model_dirty)+(SELECT COUNT(*) FROM usage_dirty)").Scan(&dirty); err != nil || dirty != 0 {
		t.Fatal("required projection work remained at HTTP startup", dirty, err)
	}
}

func TestPreparedCardsRefreshCompletedUsageJob(t *testing.T) {
	a := setup(t)
	e := recorded(t, "count", "otel", "claude", "one", "api_request", map[string]any{"gen_ai.usage.input_tokens": "5"})
	save(t, a, e)
	replacement := recorded(t, "count", "otel", "claude", "one", "api_request", map[string]any{"gen_ai.usage.input_tokens": "10"})
	if _, err := a.DB.Exec("UPDATE events SET raw=? WHERE id=?", []byte(replacement.Raw), replacement.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.runMaintenance(context.Background(), usageJob, backfillUsage, 0); err != nil {
		t.Fatal(err)
	}
	usageWant(t, sessionUsage(t, a, "claude:one", ""), "input_tokens", "10")
}
