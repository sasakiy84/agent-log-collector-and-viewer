# Skills

Browse `/skills`, select projects and a time range, then click a skill to open its matching sessions. Search names, plugins, scopes or skill paths. Sort by confirmed reads, reported uses, sessions or recent activity.

![Skill usage](ui-skills.png)

- **Reported uses** counts explicit invocation reports from Codex, Claude Code or Copilot. Same-turn reports and imported/OTEL copies are deduplicated. **—** means no invocation report was received; reads are never added to this number.
- **Reads** counts successful skill-read tool calls. Reading the same skill again in a session adds another read. Input/output records and imported/OTEL copies of a call count once, using explicit call IDs and executing-agent identity.
- **Unconfirmed** means a read was requested but its successful execution cannot be established. **Failed** means failure was confirmed. Neither contributes to Reads. These counts appear below Reads and in the tooltip.
- **Sessions** counts sessions containing invocation reports, skill-read attempts or structured skill injections. Equal names in different plugins remain separate. Injections are identified independently and do not imply a read or invocation.

Read extraction parses structured file tools, literal shell commands (including `cd … && cat`, multiline commands and shell wrappers), and literal JavaScript tool calls inside `functions.exec`. Dedicated `skills.read` calls are recognized when the package/resource identifies a skill. Code is parsed, never executed. Available-skill lists, ordinary mentions, file writes and in-place edits are excluded. Dynamic paths and ambiguous call IDs cannot establish a confirmed read. An outer script succeeding does not prove that every inner tool succeeded.

The catalog uses only evidence in the selected time range, projects, agents and origins. The same filters carry to matching sessions. Original events remain unchanged. The offline migration preserves prepared evidence. Normal ingestion updates evidence transactionally. Session detail pages reuse summaries by session revision and origin; filtered catalog queries retain exact event-based aggregation.
