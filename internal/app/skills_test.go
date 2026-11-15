package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
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
		if row.Plugin == "" && (row.Uses != 2 || row.References != 1 || row.Sessions != 1) {
			t.Fatal(row)
		}
	}
	var sessions []SessionSummary
	getJSON(t, a, "/api/sessions?skill="+url.QueryEscape("日本語-review")+"&plugin=", &sessions)
	if len(sessions) != 1 || sessions[0].ID != "codex:root" {
		t.Fatal("text mention matched actual skill filter", sessions)
	}
	p := page(t, a, "codex:root", "import", 0)
	if len(p.Skills) != 1 || p.Skills[0].Uses != 1 || p.Skills[0].References != 1 {
		t.Fatal("origin-specific skill totals", p.Skills)
	}
	getJSON(t, a, "/api/skills?q="+url.QueryEscape("/work/.codex"), &catalog)
	if catalog.Total != 1 || catalog.Skills[0].Uses != 2 {
		t.Fatal("path search changed whole-skill totals", catalog)
	}
	if _, err := a.DB.Exec("DELETE FROM events WHERE session='codex:root'"); err != nil {
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
		{`cat > /work/foo/SKILL.md`, false}, {`sed -i 's/a/b/' /work/foo/SKILL.md`, false}, {`echo "cat /work/foo/SKILL.md"`, false}, {`cat $(echo /work/foo/SKILL.md)`, false}, {`echo hi; cat /work/foo/SKILL.md`, false},
	} {
		paths := skillReadPaths(test.command)
		found := false
		for _, path := range paths {
			found = found || skillFromPath(path).Name != ""
		}
		if found != test.want {
			t.Fatalf("%q got %v", test.command, paths)
		}
	}
	e := recorded(t, "xml", "import", "codex", "root", "gen_ai.user.message", nil)
	d := Detail{Kind: "user", Blocks: []Block{{Text: "<skill>\n<name>review-core</name><path>/work/skills/review-core/SKILL.md</path>\n" + strings.Repeat("instructions", 300)}}}
	facts := extractedSkills(e, d)
	if len(facts) != 1 || facts[0].Name != "review-core" || facts[0].Evidence != "reference" {
		t.Fatal("bounded skill injection header", facts)
	}
}
func TestSkillHistoryUsesCachedReferencesAndResumes(t *testing.T) {
	a := setup(t)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var ready bool
		if err := a.DB.QueryRow("SELECT complete FROM maintenance_jobs WHERE name=?", skillsJob).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("empty history indexing never finished")
		}
		time.Sleep(time.Millisecond)
	}
	read := recorded(t, "read", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "arguments": `{"file_path":"/work/skills/review-core/SKILL.md"}`})
	save(t, a, read, skillReport(t, "native", "otel", "root", "reported", "turn", ""))
	if _, err := a.DB.Exec("UPDATE events SET raw='not JSON' WHERE id=?; DELETE FROM skill_events; UPDATE maintenance_jobs SET cursor='',complete=0 WHERE name='skills.v1'", read.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.runMaintenance(context.Background(), skillsJob, backfillSkills, 0); err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Skills []SkillSummary `json:"skills"`
		Total  int            `json:"total"`
		Ready  bool           `json:"ready"`
	}
	getJSON(t, a, "/api/skills", &catalog)
	if !catalog.Ready || catalog.Total != 2 {
		t.Fatal(catalog)
	}
}
