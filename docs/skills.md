# Codex skills

Open **Skills** for usage totals across sessions, ordered by **Most used**. The project sidebar switches between all projects and selected projects; search names, plugins, scopes or referenced paths within that selection. **Most sessions** and **Recent activity** offer alternate orders. The header shows the matching skill count and total uses across all pages.

Use the shared [time and project filters](search-filters.md) to measure usage within a range or across several projects; counts and inference use only evidence in that scope.

Click a row to open matching sessions in the selected project. Session headers retain their skill chips and event inspector. The timeline's **Skills** filter includes reported uses and indexed references, with filtering before pagination.

![Skill usage and references](ui-skills.png)

- **Uses** combines reported invocations with historical evidence. **≈** marks an estimate: for each session/skill/plugin without invocation reports, a read or structured skill injection contributes one inferred use. Repeat reads and import/OTEL copies in that session do not increase it. Hover over a count for the reported/inferred breakdown.
- **Reported invocations** counts Codex's `codex.skill_invocation` events (`skill.name`, `skill.plugin_id`, `skill.scope`, `skill.invocation_type`, `turn.id`). Reports for the same skill/plugin/session/turn count once. Without a turn ID, individual event identities are retained. OTEL reports take precedence over imported reports for the same session/skill/plugin; explicit origin views use that origin alone.
- **Referenced sessions** counts sessions with a direct `SKILL.md` read reference or a structured `<skill>` injection. A reference does not establish an invocation or successful execution. A reference-only session contributes one inferred use, regardless of the number of reads.
- **Sessions** counts the union of both kinds of evidence. Equal names in different plugins stay separate. Plain mentions, available-skill lists, file writes and in-place edits do not count as read references.

Historical references support structured Read/read_file calls and direct cat/head/tail/sed reads, including quoted paths. Shell control flow, substitutions and redirections are excluded. Stored event content stays unchanged; this is conservative evidence recovery, not an exact historical invocation count.

Existing cached history is indexed in resumable background batches after HTTP startup. New events update the index during ingestion. **Indexing history** indicates that historical totals are still being populated; Reload the page to update them. No browser polling is used. Archive deletion removes the corresponding skill evidence.

Codex emits skill metadata through its configured log exporter; no additional skill-specific setting is required. The current [Codex implementation](https://github.com/openai/codex/blob/rust-v0.169.0/codex-rs/otel/src/skill_invocation.rs) defines these fields.

```sh
AGENTLOG_BIN=./agentlog python3 scripts/skills_browser_test.py
```

Screenshots and checks use synthetic data.
