package app

import (
	"context"
	"database/sql"
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
		if _, err := tx.ExecContext(ctx, "DELETE FROM records WHERE id='duplicate-request'"); err != nil {
			return err
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	check("inner-request")
	// Losing the explicit bridge cannot leave statistics attached to a request.
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM records WHERE id IN('inner-request','bridge')"); err != nil {
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
