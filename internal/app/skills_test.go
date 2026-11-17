package app

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"
)

func skillReport(t *testing.T, id, origin, session, name, turn, plugin string) Event {
	return recorded(t, id, origin, "codex", session, "codex.skill_invocation", map[string]any{"skill.name": name, "turn.id": turn, "skill.scope": "repo", "skill.plugin_id": plugin, "skill.invocation_type": "explicit"})
}
func TestSkillsReportedUsesReferencesAndExactFiltering(t *testing.T) {
	a := setup(t)
	read := recorded(t, "read", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "exec_command", "call_id": "read", "arguments": `{"cmd":"sed -n '1,100p' /work/.codex/skills/日本語-review/SKILL.md"}`})
	write := recorded(t, "write", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "exec_command", "arguments": `{"cmd":"cat > /work/.codex/skills/not-used/SKILL.md"}`})
	mention := recorded(t, "mention", "import", "codex", "mention", "gen_ai.user.message", map[string]any{"prompt": "Available skill 日本語-review at /work/.codex/skills/not-used/SKILL.md"})
	save(t, a, read, write, mention, skillReport(t, "one", "otel", "root", "日本語-review", "turn-1", ""), skillReport(t, "retry", "otel", "root", "日本語-review", "turn-1", ""), skillReport(t, "two", "otel", "root", "日本語-review", "turn-2", ""), skillReport(t, "copy", "import", "root", "日本語-review", "turn-1", ""), skillReport(t, "plugin", "otel", "other", "日本語-review", "turn-1", "plugin-a"))
	var catalog struct {
		Skills []SkillSummary `json:"skills"`
		Total  int            `json:"total"`
	}
	getJSON(t, a, "/api/skills?q="+url.QueryEscape("日本語"), &catalog)
	if catalog.Total != 2 {
		t.Fatalf("skills: %+v", catalog)
	}
	for _, row := range catalog.Skills {
		if row.Plugin == "" && (row.Attempts != 3 || row.Sessions != 1) {
			t.Fatal(row)
		}
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?skill="+url.QueryEscape("日本語-review")+"&plugin=", &sessions)
	if len(sessions) != 1 || sessions[0].ID != "codex:root" {
		t.Fatal("text mention matched actual skill filter", sessions)
	}
	p := page(t, a, "codex:root", "import", 0)
	if len(p.Skills) != 1 || p.Skills[0].Attempts != 2 {
		t.Fatal("origin-specific skill totals", p.Skills)
	}
	getJSON(t, a, "/api/skills?q="+url.QueryEscape("/work/.codex"), &catalog)
	if catalog.Total != 1 || catalog.Skills[0].Attempts != 3 {
		t.Fatal("path search changed whole-skill totals", catalog)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE session='codex:root'"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	getJSON(t, a, "/api/skills", &catalog)
	if catalog.Total != 1 || catalog.Skills[0].Plugin != "plugin-a" {
		t.Fatal("deleted skills remained in catalog", catalog)
	}
}
func TestSkillReferenceExtractionIsConservative(t *testing.T) {
	for _, test := range []struct {
		command string
		want    bool
	}{
		{`cat '/work/my skill/SKILL.md'`, true}, {`sed -n '1,200p' /work/foo/SKILL.md`, true}, {`/bin/cat "$HOME/.codex/skills/foo/SKILL.md"`, true},
		{`cat > /work/foo/SKILL.md`, false}, {`sed -i 's/a/b/' /work/foo/SKILL.md`, false}, {`echo "cat /work/foo/SKILL.md"`, false}, {`cat $(echo /work/foo/SKILL.md)`, false}, {`echo hi; cat /work/foo/SKILL.md`, true},
	} {
		paths := shellSkillReads(test.command, "", 0)
		found := false
		for _, read := range paths {
			found = found || read.Name != ""
		}
		if found != test.want {
			t.Fatalf("%q got %v", test.command, paths)
		}
	}
	e := recorded(t, "xml", "import", "codex", "root", "gen_ai.user.message", nil)
	d := Detail{Kind: "user", Blocks: []Block{{Text: "<skill>\n<name>review-core</name><path>/work/skills/review-core/SKILL.md</path>\n" + strings.Repeat("instructions", 300)}}}
	facts := extractedSkills(e, d)
	if len(facts) != 1 || facts[0].Name != "review-core" || facts[0].Evidence != "injection" {
		t.Fatal("bounded skill injection header", facts)
	}
}

// The overview must choose invocation evidence before newer reads, keep plugin
// groups separate, and resolve OTEL IDs to their merged import counterpart.
func TestSessionSkillsRepresentativeEvidenceAndOrigins(t *testing.T) {
	a := setup(t)
	imported := recorded(t, "merged", "import", "codex", "root", "gen_ai.user.message", map[string]any{"message_id": "shared", "prompt": "hello"})
	native := recorded(t, "native", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"message_id": "shared", "prompt": "hello"})
	recent := recorded(t, "recent", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"prompt": "later"})
	recent.Time = "2026-11-15T00:00:00.000000000Z"
	native.Origin, recent.Origin = "otel", "otel"
	save(t, a, imported, native, recent)
	err := inTransaction(context.Background(), a.DB, func(tx *sql.Tx) error {
		for _, f := range []struct {
			event    Event
			evidence SkillEvidence
			group    string
		}{
			{native, SkillEvidence{Name: "review", Evidence: "reported"}, "invocation"},
			{recent, SkillEvidence{Name: "review", Evidence: "read"}, "read"},
			{recent, SkillEvidence{Name: "review", Plugin: "plugin-a", Evidence: "read"}, "plugin-read"},
		} {
			if err := insertSkillFact(context.Background(), tx, f.event, f.evidence, f.group); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"", "otel", "import"} {
		tx, err := a.readers.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := sessionSkills(context.Background(), tx, "codex:root", origin)
		tx.Rollback()
		if err != nil {
			t.Fatal(err)
		}
		if origin == "import" {
			if len(rows) != 0 {
				t.Fatal("evidence leaked across origins", rows)
			}
			continue
		}
		if len(rows) != 2 {
			t.Fatal(rows)
		}
		for _, row := range rows {
			if row.Plugin == "plugin-a" {
				if row.Event != recent.ID || row.Attempts != 1 {
					t.Fatal(row)
				}
				continue
			}
			want := imported.ID
			if origin == "otel" {
				want = native.ID
			}
			if row.Event != want || row.Attempts != 2 || row.Last != recent.Time {
				t.Fatal(row)
			}
		}
	}
}
