package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestUnchangedTraceIdentityDoesNotRebuildWholeTrace(t *testing.T) {
	a := setup(t)
	request := modelSpan(t, "request", "scope", "root", "", "run_sampling_request", nil)
	usage := modelSpan(t, "usage", "scope", "usage", "root", "handle_responses", map[string]any{"input_tokens": "123"})
	save(t, a, request, usage)
	ctx := context.Background()
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		// SQLite UPDATE OF fires for assigned columns even when values are equal.
		// The ingestion upsert assigns every identity column on content changes.
		if _, err := tx.Exec(`UPDATE records SET session=session,source=source,origin=origin,
 signal=signal,trace=trace,span=span,parent_span=parent_span WHERE id='request'`); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM pending_model_trace").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("unchanged ancestry scheduled trace-wide work", count)
		}
		return refreshProjections(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		var encoded string
		if err := tx.QueryRow("SELECT info FROM model_facts WHERE id='request'").Scan(&encoded); err != nil {
			return err
		}
		var call ModelCall
		if err := json.Unmarshal([]byte(encoded), &call); err != nil {
			return err
		}
		call.Duration = "999"
		data, err := json.Marshal(call)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE model_facts SET session=session,source=source,origin=origin,
 trace=trace,span=span,role=role,info=? WHERE id='request'`, string(data)); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRow("SELECT COUNT(*) FROM pending_model_trace").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("call statistics change scheduled unrelated ancestry work", count)
		}
		if err = refreshModelCalls(ctx, tx); err != nil {
			return err
		}
		var duration, tokens string
		if err = tx.QueryRow("SELECT json_extract(info,'$.duration_ms'),json_extract(info,'$.values.input_tokens') FROM model_calls WHERE id='request'").Scan(&duration, &tokens); err != nil {
			return err
		}
		if duration != "999" || tokens != "123" {
			t.Fatal("local call refresh lost duration or linked usage", duration, tokens)
		}
		// A real request identity change must still invalidate existing usage.
		if _, err = tx.Exec("UPDATE model_facts SET span='replacement' WHERE id='request'"); err != nil {
			return err
		}
		if err = tx.QueryRow("SELECT COUNT(*) FROM pending_model_trace").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatal("changed request did not invalidate trace ancestry", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPresentationChangesDoNotRewriteMembershipCounters(t *testing.T) {
	a := setup(t)
	imported := recorded(t, "imported", "import", "codex", "merged", "gen_ai.user.message", map[string]any{"gen_ai.message.id": "shared", "prompt": "hello"})
	native := recorded(t, "native", "otel", "codex", "merged", "gen_ai.user.message", map[string]any{"message_id": "shared", "prompt": "hello"})
	save(t, a, imported, native)
	if _, err := a.DB.Exec(`CREATE TABLE fixture_counter_writes(operation TEXT);
 CREATE TRIGGER fixture_card_count_update AFTER UPDATE ON card_counts BEGIN INSERT INTO fixture_counter_writes VALUES('update'); END;
 CREATE TRIGGER fixture_card_count_insert AFTER INSERT ON card_counts BEGIN INSERT INTO fixture_counter_writes VALUES('insert'); END;
 CREATE TRIGGER fixture_card_count_delete AFTER DELETE ON card_counts BEGIN INSERT INTO fixture_counter_writes VALUES('delete'); END;`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE record_facts SET detail=json_set(detail,'$.title','Updated display label') WHERE id='native'"); err != nil {
			return err
		}
		var writes, pending int
		if err := tx.QueryRow("SELECT COUNT(*) FROM fixture_counter_writes").Scan(&writes); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT COUNT(*) FROM pending_presentation").Scan(&pending); err != nil {
			return err
		}
		if writes != 0 || pending != 0 {
			t.Fatal("display-only update rewrote counts or logical flags", writes, pending)
		}
		// Later skill evidence on the OTEL original must update the merged card.
		if _, err := tx.Exec("UPDATE record_facts SET has_skill=1 WHERE id='native'"); err != nil {
			return err
		}
		if err := refreshPresentation(ctx, tx); err != nil {
			return err
		}
		var skill bool
		if err := tx.QueryRow("SELECT has_skill FROM logical_events WHERE id=?", imported.ID).Scan(&skill); err != nil {
			return err
		}
		if !skill {
			t.Fatal("late OTEL skill evidence did not reach merged representative")
		}
		if err := tx.QueryRow("SELECT COUNT(*) FROM fixture_counter_writes").Scan(&writes); err != nil {
			return err
		}
		if writes != 0 {
			t.Fatal("flag update rewrote unchanged membership counts", writes)
		}
		// Membership changes remain counted, even if all presentation flags match.
		if _, err := tx.Exec("UPDATE record_facts SET parent='codex:parent' WHERE id='native'"); err != nil {
			return err
		}
		var parentCount int
		if err := tx.QueryRow("SELECT count FROM card_counts WHERE session='codex:merged' AND origin='otel' AND parent='codex:parent'").Scan(&parentCount); err != nil {
			return err
		}
		if parentCount != 1 {
			t.Fatal("changed membership lost its count", parentCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
