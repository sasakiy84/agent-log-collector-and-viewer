# Export

Choose a project and click **Export project**, or open **Export** in the top bar.
The project button opens `/export?project=…&diagnostics=1` with the whole project
selected. Previous text/ID filters are cleared for this full-project action.

The export page supports project selection, Japanese/English text search, exact
IDs and agent filtering. Its URL preserves the search conditions. Counts and the
preview show the complete export scope; **Download ZIP** exports all matching
sessions, including pages beyond the one currently displayed. A text/ID match
selects a session's entire history, including replies and tool results.

By default, matching subagent and internal Codex sessions are included. Uncheck
**Internal Codex sessions** to exclude internal conversations. Project selection
uses the stored project label/path, as in the viewer; absent project metadata is
not invented. Exports contain current committed DB records, including data held
by an archive operation until deletion. Original session files are not modified.

ZIP contents:

- `manifest.json`: format/version, creation time, filters and session/record counts.
- `events.jsonl`: one original collector event per line, including ID, session,
  parent, source, origin, timestamps and the full OTLP `raw` document. Imported
  and OTEL counterparts are both included, even if the viewer merges their cards.

The ZIP streams directly to the browser without retaining an export file on the
server or assembling the full dataset in browser memory. A single read snapshot
keeps membership and counts consistent while ingestion continues. Interrupted
exports are not finalized as successful ZIPs.

Authenticated API:

- Preview: `GET /api/export/sessions?project=…&q=…`
- Download: `GET /api/export/download?project=…&q=…`

Both accept the session search filters (`project`, `folder`, `q`, `id_key`,
`id_value`, `source`, `skill`, `plugin`, `diagnostics`). Internal sessions default
to included; use `diagnostics=0` to exclude them. Preview pagination uses `offset`;
downloads ignore it. The existing single-session JSON endpoint remains available.
