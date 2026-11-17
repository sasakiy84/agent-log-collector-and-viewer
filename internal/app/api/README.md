# Agentlog API

Use `Authorization: Bearer <API_KEY>` over HTTP. API keys created in Settings grant read-only access to stored data. Only the owner login / `AGENTLOG_TOKEN` can manage keys, ingest telemetry or perform archive mutations. Keys do not work as browser login tokens. Revoking a key rejects subsequent requests immediately.

```sh
curl -fsS -H 'Authorization: Bearer <API_KEY>' '<BASE_URL>/api/readme'
curl -fsS -H 'Authorization: Bearer <API_KEY>' '<BASE_URL>/api/metrics'
```

Replace `<BASE_URL>` with the server origin and `<API_KEY>` with the key. When `AGENTLOG_TOKEN` is unset, unauthenticated local access remains enabled. API keys are still read-only when explicitly supplied. Responses are JSON unless noted. Names, IDs and search strings must be URL-encoded. Timestamps use UTC RFC3339; event timestamps retain their source precision.

## Server metrics

`GET /api/metrics`

```json
{"version":"v0.23.0","started_at":"2026-11-17T00:00:00Z","uptime_seconds":3600,"counts_ready":true,"events":650962,"sessions":13085,"logical_events":640000,"raw_bytes":123456789,"state":"ready","pending_batches":0,"pending_bytes":0,"queue_capacity_bytes":268435456,"import_capacity_bytes":134217728}
```

- `events`: stored original records, including import/OTEL copies and all signals.
- `sessions`: stored sessions, including internal and child sessions; not the visible-list count.
- `logical_events`: timeline entries after explicit import/OTEL deduplication.
- `raw_bytes`: total original JSON payload bytes; not the database file size.
- `counts_ready`: raw counters are complete. During initial preparation, incomplete counts are `null`; logical totals are `null` until views are ready.
- `state`: `migrating`, `preparing`, `ready` or `failed`.
- `pending_batches`, `pending_bytes`: telemetry durably accepted but waiting for projection. Queue values are sampled separately from the stored-data snapshot.
- Version and uptime describe the running binary/process. Counts read incremental metadata; they do not scan raw JSON.

`GET /api/status` returns just state and queue metrics. A successful OTLP POST confirms durable acceptance, not immediate indexing.

## Projects

`GET /api/projects?q=shop&offset=0`

Returns `{"projects":[{"id":"name:shop","name":"shop","count":2,"end":"..."}],"total":1,"sessions":2,"offset":0}`. Default counts include visible root sessions. Set `diagnostics=1` to include internal sessions, or `include_children=1` to include children in project counts. `q` searches project names; `session_q` filters by log content. Page size is 200. Project IDs such as `name:shop` and the empty unassigned ID are passed to session/skill filters.

## Session search

`GET /api/sessions?project=name%3Ashop&q=authentication&offset=0`

`compact=1` omits usage `basis` and alternate-origin `variants` from list responses. The default response and session details retain them.

Returns an array of session summaries: `id`, `source`, `parent`, `count`, `start`, `end`, `preview`, `usage`. These summaries describe whole sessions, even when search conditions match an earlier event. Results are ordered by last activity descending. Child sessions are reached through parents, not the ordinary list.

Pagination headers are `X-Total-Count` and `X-Page-Offset`. Pages contain up to **201 entries**: 200 results plus one lookahead entry. Consume the first 200 and advance the returned offset by 200 until reaching the total. Stale offsets clamp to a valid page.

| Query | Meaning |
| --- | --- |
| `project` | Repeat project IDs; empty value selects unassigned |
| `source` | Repeat `codex`, `claude`, `copilot`, `other` |
| `origin` | Repeat `otel`, `import` |
| `from`, `to` | Event-time bounds, inclusive start / exclusive end, RFC3339 with timezone |
| `q`, `text_mode` | Japanese/English full-text search, `all` (default) or `any` |
| `exclude` | Exclude records containing any analyzed exclusion word |
| `event_kind` | Repeat `conversation`, `tool`, `error`, `model`, `metric`, `skill`, `agent`, `event` |
| `id_key`, `id_value` | Exact identifier field and value, matched in the same record as text/time/type filters |
| `skill`, `plugin` | Exact skill and plugin; an empty plugin selects unnamespaced skills |
| `diagnostics` | `1` includes internal/generated sessions and unassigned telemetry |
| `offset` | Zero-based offset in steps of 200 |

Within a field values are ORed; fields are ANDed. Time/type/origin/text conditions apply to the same stored record. No selected values means all. Numeric and boolean telemetry attributes are not full-text indexed; IDs use exact matching rather than morphological analysis.

`GET /api/search/summary` accepts these conditions plus `group=day|project|source|kind`, returning matching-record/session/error counts for up to 100 groups. It counts original records, including distinct import/OTEL copies.

## Session content

`GET /api/session?id=<SESSION_ID>&view=conversation&position=latest`

Returns a session summary plus `total_count`, `count`, `offset`, `limit`, `view`, `events`, `processing`, `children`, `children_next` and `skills`. Conversation events are oldest to newest within the page. `position=latest` opens the newest page; use `offset=0` for the oldest page. `limit` is 1–200 (default 200). `origin=otel|import` selects one origin; omit it for merged entries.

- `view=conversation`: user inputs and assistant replies only, at most 200 events. `processing` gives collapsed ranges and their counts/errors without loading log bodies.
- `view=activity`: useful conversation/tool/model/skill activity. `kind` optionally selects an event type.
- `view=diagnostics`: every timeline entry. Non-conversation views can contain one lookahead event; use the first `limit` entries for paging.
- Event entries include `id`, `time`, `detail`, `origins` and bounded `records` metadata. They do not contain full originals. Source-dependent missing content remains missing.

`GET /api/processing?session=<SESSION_ID>&after=<ID>&before=<ID>` expands a range supplied by `processing`. Omit an absent boundary. Returns `{"events":[...],"next":"..."}`, up to 50 events. Pass `cursor=<next>` for another page; an empty next cursor ends the range. Add `diagnostics=1` for internal records, `errors=1` for only errors, and retain the selected `origin`.

`GET /api/event?session=<SESSION_ID>&id=<EVENT_ID>` returns the full inspected event and original `records[].raw`. Tool input/output counterparts and known subagent links are included when explicit identifiers establish the relationship. Use event IDs returned by this API, not inferred IDs. Retain `origin` when inspecting an origin-specific page. Add `raw=0` to omit originals from the Details response. `view=raw` returns only `{ "records": [...] }` with full original bytes, without reparsing presentations or loading relationships.

`GET /api/session/children?session=<SESSION_ID>&after=<CHILD_ID>` pages child session IDs, 20 at a time, returning `children` and `next`.

`GET /api/subagent?session=<SESSION_ID>&agent=<AGENT_ID>&position=latest` previews a Copilot agent within its parent session, 40 entries per page. Use `offset` for older pages. Agent IDs come from `subagents[].agent` on inspected events and may be `trace:<trace_id>:<span_id>`. Links with a distinct `subagents[].session` instead open that child through `/api/session`. Do not infer agent ancestry.

## Skills

`GET /api/skills?project=name%3Ashop&sort=reads&offset=0`

Supports project/source/origin/time conditions above, skill-name/plugin/scope/path `q` search, `text_mode` and `exclude`. `sort` is `reads` (default), `uses`, `sessions` or `recent`. Returns `skills`, `total`, `offset`, `sort`, `uses`, `reads` and history-index `ready`; pages contain at most 200 skills.

Each skill includes `name`, optional `plugin`, `sessions`, `uses`, `reads`, `pending`, `failed`, `injections`, `last`, and `scopes`. `uses` counts explicit invocation reports; it never infers invocation from a read. `reads` counts confirmed successful read calls, including repeated calls in one session. Pending/failed attempts and structured injections are separate. Counts use only evidence in the selected range/origin.

## Export

- `GET /api/export?session=<SESSION_ID>&origin=otel`: streamed JSON array of complete original records for one session. Omit origin to include both.
- `GET /api/export/sessions`: matching sessions for export, using session filters. Includes internal/child sessions by default; `include_children=0` restricts to roots. Pagination headers also include `X-Record-Count`.
- `GET /api/export/download`: ZIP of complete histories of matching sessions, using the export filters. The ZIP includes a manifest. Histories include records outside the search range. Exports use bounded concurrency and streaming.

## Owner administration

These require the owner cookie or `AGENTLOG_TOKEN`, not a read-only API key:

- `GET /api/settings/api-keys`: list recoverable names/keys/creation times.
- `POST /api/settings/api-keys` with JSON `{"name":"My agent"}`: create a key (`201`). Names contain 1–120 characters.
- `PATCH /api/settings/api-keys/<ID>` with JSON `{"name":"New name"}`: rename (`204`).
- `DELETE /api/settings/api-keys/<ID>`: revoke/delete (`204`).
- `POST /v1/logs`, `/v1/traces`, `/v1/metrics`: OTLP/HTTP JSON or protobuf, using the collection token.
- Archive creation, deletion, cancellation and compaction remain owner operations. Read-only keys can inspect/download existing archives through the existing GET endpoints.

## Errors

`400`: invalid parameters/body. `401`: missing, invalid or revoked credentials. `403`: read-only key used for management or a mutation. `404`: resource absent. `409`: session/archive changed or locked; reload the session or reopen the range. `429`: export concurrency limit. `503`: database initialization or ingestion capacity; honor `Retry-After` when present. Error bodies are plain text. Keep keys in the Authorization header, never query parameters.
