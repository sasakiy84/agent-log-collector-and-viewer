package app

import (
	"encoding/json"
	"fmt"
	"testing"
)

func skillReadEvents(t *testing.T, id, origin, source, session, tool, args string, attrs map[string]any) (Event, Event) {
	t.Helper()
	in := map[string]any{"tool_name": tool, "call_id": id, "arguments": args}
	out := map[string]any{"call_id": id, "success": true, "output": "skill instructions"}
	for k, v := range attrs {
		in[k] = v
		out[k] = v
	}
	return recorded(t, id+"-in", origin, source, session, "gen_ai.tool.start", in), recorded(t, id+"-out", origin, source, session, "gen_ai.tool.result", out)
}
func skillCounts(t *testing.T, a *App, query string) SkillSummary {
	t.Helper()
	var p skillCatalogPage
	getJSON(t, a, "/api/skills"+query, &p)
	if len(p.Skills) != 1 {
		t.Fatalf("expected one skill: %+v", p)
	}
	return p.Skills[0]
}
func TestSkillReadsCountRepeatedCallsPairLateAndDeduplicateOrigins(t *testing.T) {
	a := setup(t)
	input, output := skillReadEvents(t, "call-1", "import", "codex", "root", "exec_command", `{"cmd":"cd /work/skills/review && cat SKILL.md"}`, nil)
	save(t, a, input)
	if s := skillCounts(t, a, ""); s.Uses != 0 || s.Reads != 0 || s.Pending != 1 {
		t.Fatal(s)
	}
	save(t, a, output)
	if s := skillCounts(t, a, ""); s.Reads != 1 || s.Pending != 0 || s.Sessions != 1 {
		t.Fatal(s)
	}
	next, result := skillReadEvents(t, "call-2", "import", "codex", "root", "Read", `{"file_path":"/work/skills/review/SKILL.md"}`, nil)
	// Output can arrive before its corresponding input.
	save(t, a, result)
	save(t, a, next)
	native, nativeResult := skillReadEvents(t, "call-1", "otel", "codex", "root", "exec_command", `{"cmd":"cd /work/skills/review && cat SKILL.md"}`, nil)
	save(t, a, native, nativeResult)
	if s := skillCounts(t, a, ""); s.Reads != 2 || s.Pending != 0 || s.Sessions != 1 || s.Uses != 0 {
		t.Fatal(s)
	}
	for _, origin := range []string{"import", "otel"} {
		p := page(t, a, "codex:root", origin, 0)
		want := 2
		if origin == "otel" {
			want = 1
		}
		if len(p.Skills) != 1 || p.Skills[0].Reads != want {
			t.Fatal(origin, p.Skills)
		}
	}
	// A result update and deletion recompute existing reads, without reparsing GETs.
	failed := recorded(t, "call-2-out", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "call-2", "success": false})
	save(t, a, failed)
	if s := skillCounts(t, a, ""); s.Reads != 1 || s.Failed != 1 {
		t.Fatal(s)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", failed.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	if s := skillCounts(t, a, ""); s.Reads != 1 || s.Pending != 1 || s.Failed != 0 {
		t.Fatal(s)
	}
}
func TestSkillReadIDsAreAgentScopedAndAmbiguityIsNotSuccess(t *testing.T) {
	a := setup(t)
	input, output := skillReadEvents(t, "same", "otel", "codex", "root", "Read", `{"file_path":"/work/skills/review/SKILL.md"}`, map[string]any{"agent_id": "one"})
	_, wrong := skillReadEvents(t, "wrong", "otel", "codex", "root", "Read", `{}`, map[string]any{"call_id": "same", "agent_id": "two"})
	save(t, a, input, wrong)
	if s := skillCounts(t, a, ""); s.Reads != 0 || s.Pending != 1 {
		t.Fatal(s)
	}
	save(t, a, output)
	if s := skillCounts(t, a, ""); s.Reads != 1 {
		t.Fatal(s)
	}
	// Reusing an input ID within the same execution scope is ambiguous.
	duplicate := recorded(t, "other-input", "otel", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "Read", "call_id": "same", "agent_id": "one", "arguments": `{"path":"/work/skills/review/SKILL.md"}`})
	save(t, a, duplicate)
	if s := skillCounts(t, a, ""); s.Reads != 0 || s.Pending != 2 {
		t.Fatal(s)
	}
}
func TestSkillParsersRecoverLiteralReadsWithoutExecutingCode(t *testing.T) {
	for _, test := range []struct {
		code    string
		script  bool
		name    string
		certain bool
	}{
		{`cd /work/skills/review && cat SKILL.md`, false, "review", true},
		{"echo hi\ncat /work/skills/review/SKILL.md", false, "review", true},
		{`cat /work/skills/review/SKILL.md | head`, false, "review", false},
		{`false || cat /work/skills/review/SKILL.md`, false, "review", false},
		{`bash -lc 'cat /work/skills/review/SKILL.md'`, false, "review", true},
		{`echo 'cat /work/skills/review/SKILL.md'`, false, "", false},
		{`sed -i 's/x/y/' /work/skills/review/SKILL.md`, false, "", false},
		{`cat $(get_path)/SKILL.md`, false, "", false},
		{`text(await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md",max_output_tokens:1000}));`, true, "review", true},
		{`const result = await tools.exec_command({cmd:"cd /work/skills/review && cat SKILL.md"}); text(result);`, true, "review", true},
		{`await tools.skills__read({package:"skill://plugin/review"})`, true, "review", true},
		{`if (ready) await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md"});`, true, "review", false},
		{`await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md"});await tools.exec_command({cmd:"echo done"});`, true, "review", false},
		{`function unused(){tools.exec_command({cmd:"cat /work/skills/review/SKILL.md"})}`, true, "", false},
		{`await tools.exec_command({cmd:makeCommand()});`, true, "", false},
		{`await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md",...override});`, true, "", false},
	} {
		t.Run(test.code, func(t *testing.T) {
			var reads []skillRead
			if test.script {
				reads = scriptSkillReads(test.code, "")
			} else {
				reads = shellSkillReads(test.code, "", 0)
			}
			if test.name == "" {
				if len(reads) != 0 {
					t.Fatal(reads)
				}
				return
			}
			if len(reads) != 1 || reads[0].Name != test.name || reads[0].Certain != test.certain {
				t.Fatal(reads)
			}
		})
	}
}
func TestSkillScriptRequiresInnerSuccessAndReadToolsRequireResult(t *testing.T) {
	a := setup(t)
	args := `text(await tools.exec_command({cmd:"cat /work/skills/review/SKILL.md"}));`
	input := recorded(t, "script-in", "import", "codex", "root", "gen_ai.tool.start", map[string]any{"tool_name": "functions.exec", "call_id": "script", "arguments": args})
	result := func(body string) Event {
		return recorded(t, "script-out", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "script", "success": true, "output": body})
	}
	save(t, a, input, result("Script completed\nWall time 1 seconds\nOutput:\n"))
	if s := skillCounts(t, a, ""); s.Pending != 1 || s.Reads != 0 {
		t.Fatal(s)
	}
	save(t, a, result("Script completed\nWall time 1 seconds\nOutput:\n{\"exit_code\":0,\"output\":\"instructions\"}"))
	if s := skillCounts(t, a, ""); s.Reads != 1 || s.Pending != 0 {
		t.Fatal(s)
	}
	save(t, a, result("Script completed\nWall time 1 seconds\nOutput:\n{\"exit_code\":1,\"output\":\"missing\"}"))
	if s := skillCounts(t, a, ""); s.Failed != 1 || s.Reads != 0 {
		t.Fatal(s)
	}
}
func TestSkillReadCountsAcrossLargeBatch(t *testing.T) {
	a := setup(t)
	for i := 0; i < 130; i++ {
		input, output := skillReadEvents(t, fmt.Sprintf("read-%03d", i), "import", "codex", "root", "exec_command", `{"cmd":"cd /work/skills/review && cat SKILL.md"}`, nil)
		save(t, a, input, output)
	}
	s := skillCounts(t, a, "")
	if s.Reads != 130 || s.Pending != 0 || s.Uses != 0 {
		b, _ := json.Marshal(s)
		t.Fatal(string(b))
	}
}

func TestSkillReadCanFinishOverOTELAfterImportedInput(t *testing.T) {
	a := setup(t)
	in, _ := skillReadEvents(t, "mixed", "import", "codex", "root", "Read", `{"file_path":"/work/skills/review/SKILL.md"}`, nil)
	_, out := skillReadEvents(t, "mixed", "otel", "codex", "root", "Read", `{}`, nil)
	save(t, a, in)
	save(t, a, out)
	if s := skillCounts(t, a, ""); s.Reads != 1 || s.Pending != 0 {
		t.Fatal(s)
	}
	p := page(t, a, "codex:root", "import", 0)
	if len(p.Skills) != 1 || p.Skills[0].Reads != 1 {
		t.Fatal(p.Skills)
	}
}
