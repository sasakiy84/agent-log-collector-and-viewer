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

func TestTraceReconciliationUsesExplicitParentWhenAmbiguous(t *testing.T) {
	a := setup(t)
	ambiguousTraceFixture(t, a)
	child := recorded(t, "child", "otel", "codex", "unused", "handle_responses", nil)
	child.Trace, child.Span, child.ParentSpan, child.Session = "shared", "child-span", "pb", "trace:codex:shared"
	save(t, a, child)
	for id, want := range map[string]string{"child": "codex:b", "pending": "trace:codex:shared"} {
		var session string
		if err := a.DB.QueryRow("SELECT session FROM events WHERE id=?", id).Scan(&session); err != nil || session != want {
			t.Fatalf("%s: got %q, want %q: %v", id, session, want, err)
		}
	}
}

func TestTraceReconciliationFallbackIgnoresUnassignedAndOtherSources(t *testing.T) {
	a := setup(t)
	owner := recorded(t, "owner", "otel", "codex", "root", "user_prompt", map[string]any{"prompt": "Hello"})
	owner.Trace, owner.Span = "shared", "owner-span"
	other := recorded(t, "other", "otel", "claude", "other", "user_prompt", map[string]any{"prompt": "Other"})
	other.Trace, other.Span = "shared", "other-span"
	unassigned := recorded(t, "unassigned", "otel", "codex", "unused", "event", nil)
	unassigned.Trace, unassigned.Session = "shared", "unassigned:codex"
	child := recorded(t, "child", "otel", "codex", "unused", "receiving", nil)
	child.Trace, child.ParentSpan, child.Session = "shared", "missing", "trace:codex:shared"
	save(t, a, owner, other, unassigned, child)
	var session string
	if err := a.DB.QueryRow("SELECT session FROM events WHERE id='child'").Scan(&session); err != nil || session != "codex:root" {
		t.Fatal("unambiguous fallback lost", session, err)
	}
}

// Match a long Codex streaming trace and an OTLP batch of receiving /
// handle_responses spans. No raw payloads are needed to measure ownership work.
func BenchmarkReconcileStreamingTrace(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE events(id TEXT PRIMARY KEY,source TEXT,trace TEXT,session TEXT,span TEXT,parent_span TEXT);
 CREATE INDEX events_trace ON events(source,trace,span);
 CREATE INDEX events_pending_trace ON events(source,trace,parent_span) WHERE trace<>'' AND session GLOB 'trace:*';
 CREATE TABLE trace_dirty(source TEXT,trace TEXT,PRIMARY KEY(source,trace)) WITHOUT ROWID;
 CREATE TABLE archive_locks(session TEXT PRIMARY KEY);
 WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<120000)
 INSERT INTO events SELECT printf('%d',x),'codex','shared','codex:root',printf('%016x',x),'' FROM n;`)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		_, err = tx.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<418)
 INSERT INTO events SELECT printf('p%d',x),'codex','shared','trace:codex:shared',printf('p%d',x),printf('%016x',x) FROM n;
 INSERT INTO trace_dirty VALUES('codex','shared');`)
		if err == nil {
			_, err = reconcileTraceBatch(context.Background(), tx)
		}
		tx.Rollback()
		if err != nil {
			b.Fatal(err)
		}
	}
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
	b, err := Open(context.Background(), path, token)
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
