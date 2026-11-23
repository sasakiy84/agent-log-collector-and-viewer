package app

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestSkillCatalogPeriodsDeduplicateWithinEachWindow(t *testing.T) {
	a := setup(t)
	now := time.Now().UTC()
	at := func(days int) string { return now.AddDate(0, 0, -days).Format("2006-01-02T15:04:05.000000000Z") }
	outer := recorded(t, "period-wrapper", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "functions.exec", "call_id": "outer-call", "arguments": `text(await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md"}));`})
	result := recorded(t, "period-result", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "outer-call", "output": "Script completed\nOutput:\n{\"chunk_id\":\"period-chunk\",\"exit_code\":0,\"output\":\"content\"}"})
	inner := recorded(t, "period-tool", "otel", "codex", "root", "codex.tool_result", map[string]any{"tool_name": "exec_command", "call_id": "inner-call", "arguments": `{"cmd":"cat /work/skills/review/SKILL.md"}`, "output": "Chunk ID: period-chunk\nProcess exited with code 0\nOutput:\ncontent"})
	monthly := skillReport(t, "period-month", "otel", "root", "review", "monthly", "")
	old := skillReport(t, "period-old", "otel", "root", "review", "old", "")
	plugin := skillReport(t, "period-plugin", "otel", "root", "review", "plugin", "other")
	outer.Time, result.Time, inner.Time = at(1), at(1), at(8)
	monthly.Time, old.Time, plugin.Time = at(15), at(31), at(1)
	save(t, a, outer, result, inner, monthly, old, plugin)
	for _, test := range []struct {
		query                     string
		attempts, weekly, monthly int
		plugins                   int
	}{
		{"", 3, 1, 2, 2},
		{"&origin=import", 1, 1, 1, 1},
		{"&to=" + url.QueryEscape(at(2)), 3, 0, 2, 1},
		{"&to=" + url.QueryEscape(at(30)), 1, 0, 0, 1},
	} {
		var page struct{ Skills []skillCatalogRow }
		getJSON(t, a, "/api/skills?q=review"+test.query, &page)
		if len(page.Skills) != test.plugins {
			t.Fatalf("%s: %+v", test.query, page.Skills)
		}
		for _, skill := range page.Skills {
			if skill.Plugin == "other" {
				if skill.Weekly != 1 || skill.Monthly != 1 {
					t.Fatalf("plugin counts: %+v", skill)
				}
			} else if skill.Attempts != test.attempts || skill.Weekly != test.weekly || skill.Monthly != test.monthly {
				t.Fatalf("%s: %+v", test.query, skill)
			}
		}
	}
}

func TestSkillAttemptsDeduplicateWrapperAndConcreteExecution(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrapper first", true: "tool first"}[reverse], func(t *testing.T) {
			a := setup(t)
			args := `{"cmd":"cat /work/skills/review/SKILL.md"}`
			inner := recorded(t, "inner", "otel", "codex", "root", "codex.tool_result", map[string]any{"tool_name": "exec_command", "call_id": "inner-call", "arguments": args, "output": "Chunk ID: execution-one\nWall time: 0 seconds\nProcess exited with code 1\nOutput:\nmissing file"})
			outer := recorded(t, "outer", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "functions.exec", "call_id": "outer-call", "arguments": `text(await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md"}));`})
			result := recorded(t, "outer-result", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "outer-call", "output": "Script completed\nWall time 1 seconds\nOutput:\n{\"chunk_id\":\"execution-one\",\"exit_code\":1,\"output\":\"missing file\"}"})
			if reverse {
				save(t, a, inner)
				save(t, a, result, outer)
			} else {
				save(t, a, outer, result)
				save(t, a, inner)
			}
			for _, query := range []string{"", "?origin=import", "?origin=otel"} {
				if s := skillCounts(t, a, query); s.Attempts != 1 {
					t.Fatal(query, s)
				}
			}
			for _, origin := range []string{"", "import", "otel"} {
				p := page(t, a, "codex:root", origin, 0)
				if len(p.Skills) != 1 || p.Skills[0].Attempts != 1 {
					t.Fatal(origin, p.Skills)
				}
			}
			// A genuine retry is still a second attempt, even with identical arguments.
			retry := recorded(t, "retry", "otel", "codex", "root", "codex.tool_result", map[string]any{"tool_name": "exec_command", "call_id": "retry-call", "arguments": args, "output": "Chunk ID: execution-two\nProcess exited with code 0\nOutput:\ninstructions"})
			save(t, a, retry)
			if s := skillCounts(t, a, ""); s.Attempts != 2 {
				t.Fatal(s)
			}
			// Removing the concrete record restores the wrapper rather than losing an attempt.
			if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", inner.ID); err != nil {
				t.Fatal(err)
			}
			save(t, a)
			if s := skillCounts(t, a, ""); s.Attempts != 2 {
				t.Fatal(s)
			}
			var body map[string]json.RawMessage
			getJSON(t, a, "/api/skills?q="+url.QueryEscape("review"), &body)
			for _, old := range []string{"uses", "reads", "pending", "failed", "injections"} {
				if body[old] != nil {
					t.Fatal("obsolete counter", old)
				}
			}
		})
	}
}

func TestSkillAttemptChunkExtractionIgnoresPrintedFileContents(t *testing.T) {
	d := Detail{Title: "exec_command", Blocks: []Block{{Label: "Output", Text: "Chunk ID: actual\nProcess exited with code 0\nOutput:\nChunk ID: invented\n{\"chunk_id\":\"also-invented\"}"}}}
	chunks := skillExecutionChunks(d)
	if len(chunks) != 1 || chunks[0] != "actual" {
		t.Fatal(chunks)
	}
	d.Title = "Read"
	if chunks := skillExecutionChunks(d); len(chunks) != 0 {
		t.Fatal("file contents supplied an execution identity", chunks)
	}
}

func TestSkillAttemptMigrationRebuildsExistingEvidenceWithoutChangingOriginals(t *testing.T) {
	path := t.TempDir() + "/upgrade.db"
	a, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	input, output := skillReadEvents(t, "existing", "import", "codex", "root", "exec_command", `{"cmd":"cat /work/skills/review/SKILL.md"}`, map[string]any{"output": "Chunk ID: existing-chunk\nProcess exited with code 1\nOutput:\nmissing"})
	save(t, a, input, output)
	// Simulate the preceding generation-2 release, including its stored outcome.
	if _, err = a.DB.Exec(undoDirectDeleteMigration + `UPDATE skill_events SET evidence='failed';
 DROP TRIGGER skill_attempt_changed; DROP TABLE skill_attempt_links;
 DROP TRIGGER session_visibility_conversation; DROP INDEX codex_session_prewarm;
 ALTER TABLE session_evidence DROP COLUMN prewarm;
 DELETE FROM schema_migrations WHERE version>=3;`); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if s := skillCounts(t, a, ""); s.Attempts != 1 {
		t.Fatal(s)
	}
	var pending, links int
	if err = a.DB.QueryRow("SELECT COUNT(*) FROM pending_skill_reads").Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
	if err = a.DB.QueryRow("SELECT COUNT(*) FROM skill_attempt_links").Scan(&links); err != nil || links != 1 {
		t.Fatal(links, err)
	}
	for _, e := range []Event{input, output} {
		var raw []byte
		if err = a.DB.QueryRow("SELECT raw FROM records WHERE id=?", e.ID).Scan(&raw); err != nil || string(raw) != string(e.Raw) {
			t.Fatal("original changed", e.ID, err)
		}
	}
}

func TestSkillInjectionCopiesCountOnceButDistinctMessagesRemainSeparate(t *testing.T) {
	a := setup(t)
	prompt := "<skill><name>review</name><path>/work/skills/review/SKILL.md</path></skill>"
	imported := recorded(t, "injection-import", "import", "codex", "root", "gen_ai.user.message", map[string]any{"message_id": "shared-message", "prompt": prompt})
	native := recorded(t, "injection-native", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"message_id": "shared-message", "prompt": prompt})
	save(t, a, imported, native)
	for _, query := range []string{"", "?origin=import", "?origin=otel"} {
		if s := skillCounts(t, a, query); s.Attempts != 1 {
			t.Fatal(query, s)
		}
	}
	next := recorded(t, "injection-next", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"message_id": "next-message", "prompt": prompt})
	save(t, a, next)
	if s := skillCounts(t, a, ""); s.Attempts != 2 {
		t.Fatal(s)
	}
}
