# Codex skills

Open **Skills** to search names, plugins, scopes and referenced paths. Click a row to browse matching sessions by folder. Session headers show skill chips; clicking one opens the latest matching event in the inspector. The timeline's **Skills** filter includes skill reports and indexed references on the current page.

![Skill usage and references](ui-skills.png)

- **Reported uses** counts Codex's `codex.skill_invocation` events (`skill.name`, `skill.plugin_id`, `skill.scope`, `skill.invocation_type`, `turn.id`). Reports for the same skill/plugin/session/turn count once. Without a turn ID, individual event identities are retained. OTEL reports take precedence over imported reports for the same session/skill/plugin; explicit origin views use that origin alone.
- **Referenced sessions** counts sessions with a direct `SKILL.md` read reference or a structured `<skill>` injection. A reference does not establish an invocation or successful execution. Reference-only skills show **—** for reported uses.
- **Sessions** counts the union of both kinds of evidence. Equal names in different plugins stay separate. Plain mentions, available-skill lists, file writes and in-place edits do not count as read references.

Historical references support structured Read/read_file calls and direct cat/head/tail/sed reads, including quoted paths. Shell control flow, substitutions and redirections are excluded. Stored event content stays unchanged; this is conservative evidence recovery, not an exact historical invocation count.

Existing cached history is indexed in resumable background batches after HTTP startup. New events update the index during ingestion. **Indexing history** indicates that historical totals are still being populated; **Refresh** reloads them. No browser polling is used. Archive deletion removes the corresponding skill evidence.

Codex emits skill metadata through its configured log exporter; no additional skill-specific setting is required. The current [Codex implementation](https://github.com/openai/codex/blob/rust-v0.169.0/codex-rs/otel/src/skill_invocation.rs) defines these fields.

```sh
AGENTLOG_BIN=./agentlog python3 scripts/skills_browser_test.py
```

Screenshots and checks use synthetic data.
