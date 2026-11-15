package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestModelCallsReconcileChangedAncestry(t *testing.T) {
	a := setup(t)
	outer := modelSpan(t, "outer", "late", "outer", "", "run_sampling_request", nil)
	bridge := modelSpan(t, "bridge", "late", "inner", "outer", "transport", nil)
	usage := modelSpan(t, "usage", "late", "usage", "inner", "handle_responses", map[string]any{"input_tokens": "123"})
	save(t, a, outer, bridge, usage)
	check := func(want string) {
		t.Helper()
		var root string
		if err := a.DB.QueryRow("SELECT root FROM model_facts WHERE id='usage'").Scan(&root); err != nil || root != want {
			t.Fatalf("usage assigned to %q, want %q: %v", root, want, err)
		}
		for _, id := range []string{"outer", "inner-request"} {
			var e TimelineEvent
			getJSON(t, a, "/api/event?session=codex:late&id="+id, &e)
			if len(e.ModelCalls) != 1 {
				t.Fatal("missing model call", e)
			}
			value := e.ModelCalls[0].Values["input_tokens"]
			if (id == want && value != "123") || (id != want && value != "") {
				t.Fatal("stale per-call statistics", id, value)
			}
		}
	}
	inner := modelSpan(t, "inner-request", "late", "inner", "outer", "run_sampling_request", nil)
	// A later, closer request must supersede a previously resolved ancestor.
	save(t, a, inner)
	check("inner-request")
	// An ambiguous repeated span must remove a previous attribution.
	save(t, a, modelSpan(t, "duplicate-request", "late", "inner", "outer", "run_sampling_request", nil))
	check("")
	// Removing the conflicting report restores the unique relationship.
	ctx := context.Background()
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id='duplicate-request'"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	check("inner-request")
	// Losing the explicit bridge cannot leave statistics attached to a request.
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id IN('inner-request','bridge')"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	var root string
	if err := a.DB.QueryRow("SELECT root FROM model_facts WHERE id='usage'").Scan(&root); err != nil || root != "" {
		t.Fatal("removed ancestry retained attribution", root, err)
	}
}

func TestModelAncestryUpgradeRepairsExistingRelationshipsOnce(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t, filepath.Join(t.TempDir(), "before-v9.db"))
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan[:8]); err != nil {
		t.Fatal(err)
	}
	if err = inTransaction(ctx, db, func(tx *sql.Tx) error {
		for _, e := range []Event{
			modelSpan(t, "outer", "upgrade", "outer", "", "run_sampling_request", nil),
			modelSpan(t, "inner", "upgrade", "inner", "outer", "run_sampling_request", nil),
			modelSpan(t, "usage", "upgrade", "usage", "inner", "handle_responses", map[string]any{"input_tokens": "123"}),
		} {
			e.Origin = "otel"
			if _, err := tx.ExecContext(ctx, `INSERT INTO events(id,session,parent,source,signal,time,trace,span,parent_span,origin,kind,tool_name,name,raw)
 VALUES(?,?,'',?,?,?,?,?,?,'otel','model','',?,?)`, e.ID, e.Session, e.Source, e.Signal, e.Time, e.Trace, e.Span, e.ParentSpan, e.Name, []byte(e.Raw)); err != nil {
				return err
			}
			doc, err := decodeDocument(e.Raw)
			if err != nil {
				return err
			}
			if err = storeModelFact(ctx, tx, e, doc, describe(e, doc)); err != nil {
				return err
			}
		}
		// Reproduce the old completed state, with no pending work to discover it.
		_, err := tx.ExecContext(ctx, "UPDATE model_facts SET root='outer' WHERE id='usage';DELETE FROM model_dirty")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	if err = inTransaction(ctx, db, func(tx *sql.Tx) error { return refreshModelCalls(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	var root string
	if err = db.QueryRow("SELECT root FROM model_facts WHERE id='usage'").Scan(&root); err != nil || root != "inner" {
		t.Fatal("upgrade retained stale attribution", root, err)
	}
	if err = migrate(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = db.QueryRow("SELECT (SELECT COUNT(*) FROM model_trace_dirty)+(SELECT COUNT(*) FROM model_dirty)").Scan(&pending); err != nil || pending != 0 {
		t.Fatal("completed model upgrade was scheduled again", pending, err)
	}
}
