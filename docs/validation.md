# Reported usage (current)

- Incremental, exact-decimal accounting groups repeated responses and cumulative epochs, rejects overlapping delta intervals and conflicting reports, and caches per-session/origin totals. Folder reads combine those caches. Error counts follow logical timeline reconciliation and archive deletion.
- Coverage includes zero/missing values, integers above JavaScript's safe range, tiny USD precision, inclusive Anthropic input, Codex repeated totals, reasoning/cache subsets, origin/signal alternatives, out-of-order intervals, second/day boundaries, deletion cleanup, resumable upgrade and full-history/filter scopes.
- Chromium exercises folder/session figures, latest-position access, origin changes, cache ratio, USD, request time, error events, old Codex counter replay/labels, exact BigInt display and mobile bounds. Existing pagination/timeline/inspector flows continue to pass.
- Against the published v0.6.0 binary, isolated Codex/Claude archive fixtures confirm unchanged files remain deleted across normalization changes. Current archives suppress both client formats while allowing changes confined to new counters/response IDs. Independent review findings are fixed.
- [Rules](usage.md) · [Usage screenshot](ui-usage-session.png) · [Mobile](ui-usage-mobile.png). All screenshots use synthetic telemetry.

# Folder overview (current)

- Default navigation shows one row per full working-directory path, including all matching sessions before pagination. Each row shows its agents, session count and latest activity. Folder/session ordering is newest activity first; unknown metadata is one group, and distinct paths with the same basename stay separate.
- Opening a folder retains agent/search filters and uses server-side exact-path pagination. Reload and detail-to-list links retain the folder and page. Folder requests count and fetch from one read snapshot.
- Tests cover over 200 sessions in one group, over 200 groups, Japanese search, source filters, unknown/fallback metadata, incremental update/delete, stale offsets, long paths with the same clipped prefix, old database upgrade and oversized/non-string metadata. Normal pages remain bounded and raw records are preserved.
- Chromium covers overview/drill-down, scoped pagination/reload/back links, failed filter rollback, XSS escaping and mobile layouts, alongside the existing timeline, pagination, search, import/OTEL and UI/inspector suites. Go race tests and vet pass. Screenshots use synthetic telemetry.

# Numbered pagination (current)

- [Desktop example](ui-pagination.png) · [Mobile example](ui-pagination-mobile.png). Screenshots use synthetic telemetry.
- A shared component handles session lists, chronological timelines and archive selection: range/total, first/last/current page, ellipses, direct page input, native keyboard controls and a compact sticky layout.
- Detail pages use fixed 200-event boundaries, open at the bottom of the last page through a bounded reverse index read, and preserve historical page/origin links on reload. Historical refresh preserves the reading anchor.
- `scripts/pagination_browser_test.py` covers 1,002 sessions and 1,805 timeline events: endpoints, direct jumps, invalid/empty/fractional page input, filtered/empty totals, delayed/failed requests, disabled navigation, keyboard focus, URL reload, archive selection across pages and 320/390px layouts. The real component also renders large (up to 100 million entries) totals to check multi-digit page labels without allocating that fixture.
- Timeline/edge, full UI/inspector, Japanese search, import/OTEL and archive browser suites pass. The archive suite uses an isolated synthetic database. Race tests, vet and both JavaScript syntax checks pass. No database migration or automatic refresh was introduced.

# Japanese full-text search (current)

- Built-in FTS5 contentless-delete indexes, Kagome/IPADIC search-mode analysis and standard Lucene-style Japanese filters; NFKC/case/diacritic normalization and safe literal AND/prefix queries.
- Coverage includes one/two-character words, conjugation, width/dakuten, full untruncated content, embedded JSON keys/values and escapes, mixed agent/project labels, updated or moved events, rollback/delete, upgrade resume, archive/replay suppression, incremental posting cleanup and FTS integrity.
- Query plans use the FTS posting index and document primary keys. The native 3 MiB Japanese regression checks the final word and peak RSS; Chromium verifies composition events commit before issuing a search.
- [Settings and measurements](search.md) · [Memory regression](../scripts/search_memory_test.py) · [Japanese browser regression](../scripts/search_browser_test.py).

# Session-view performance and manual refresh (current)

- Removed all periodic browser refresh requests, the auto-refresh control, and automatic retry messaging. The Refresh button reloads explicitly, including archive progress.
- Added transactionally maintained indexed session summaries, bounded cards, incremental mixed-origin reconciliation, separate read-only WAL connections, and a lazy full-event endpoint. No original JSON is returned by list/detail pages.
- Tests compare incremental projections against a full projection after 75 identifier updates, ambiguity changes and session moves; verify original-data preservation, size limits, origin isolation, read/write concurrency, restart and archive cleanup.
- Race tests, vet, browser suites and manual-refresh/no-background-request checks pass. Updated screenshots use only synthetic telemetry. [Benchmark procedure and measured results](performance.md).

The following sections record earlier validation and may describe behavior that has since changed.

# Large Docker histories (unreleased)

- Reproduced the reported session-list failure with a sampled transcript set scaled to 147,492 stored records. The same image/database returned HTTP 500 with a 64 MiB `/tmp`, and HTTP 200 with 512 MiB. Keeping `/tmp` at 64 MiB and setting `SQLITE_TMPDIR=/data` also returned HTTP 200: SQLite's temporary query files exceeded the tmpfs limit.
- Compose now puts SQLite temporary files on the writable data volume. The non-root user, read-only root filesystem and bounded `/tmp` mount are retained. `scripts/docker_smoke.py` verifies list/detail pagination with 150,000 synthetic records in that configuration; no user transcript content is included in the fixture.
- Watch scans report unreadable directories while continuing with readable siblings. One-time import still reports scan failures. Database errors now log their underlying cause without authentication headers/query values, and UI errors include the HTTP status.
- Go race tests, vet, JavaScript syntax, Docker build and the 150,000-record Docker regression pass. The supplied sample also renders list/detail in Chromium, including automatic recovery after an HTTP 500. Independent review found no outstanding major issues.

# Event reconciliation (unreleased)

- Split HTTP/auth, storage, OTLP decoding, semantic display decoding and timeline projection. Removed the duplicate browser-side parser and the old events endpoint; the session API returns metadata and a page from one read transaction. The database uses the current schema directly without compatibility migrations.
- Preserve native event/message IDs during import and match only a one-to-one import/OTEL pair with a shared typed identifier, matching session/agent/phase, compatible tool name and no conflicting identifier namespace. Raw records are retained, independently exportable and visible in the inspector. Model context, metrics and uncertain matches remain separate.
- Tests cover both arrival orders, updated imports, trace-session reassignment, ID namespaces/aliases, ambiguous candidates, agent scopes, phases/completed spans, 205 logical events across page boundaries, list previews/counts/search, origin-specific details/export, missing fields, false/zero values, conflict reporting and message/output preservation.
- Browser tests exercise actual transcript import plus native OTEL: merged content/timing, original JSON, conflicts, origin isolation, polling selection when an OTEL-first event gains an imported counterpart, labels/usage, pagination, hostile text and mobile bounds. Folder watch process tests cover local and HTTP modes.
- Projection CTEs materialize metadata only; raw payloads are fetched after page/preview selection, or for explicit content search/export. Review findings are fixed with regression coverage.
- Two independent reviewers repeated their reviews after fixes and found no remaining P1/P2 issues. A synthetic 3,000-record, roughly 189 MiB database improved session-list reads from 3.50 s to 0.085–0.091 s after removing raw-payload materialization.
- Final validation passed: `go test -race ./...`, `go vet ./...`, JavaScript syntax, six cross-builds, the full UI/inspector suite, import/OTEL reconciliation and origin-filter browser tests, pagination/hostile-text browser tests, local/HTTP watcher processes, Docker Compose authentication/persistence and mounted per-agent session folders. Only Linux binaries were executed; other targets were cross-compiled.

# Folder watching (unreleased)

- `agentlog --config` watches folders into local SQLite; `agentlog import --watch` sends the same converted OTLP records to an existing collector. No additional dependencies. Polling defaults to one second.
- Watch tests cover initial backfill, append, unfinished JSON, new nested/missing folders, file replacement/truncation, retry without another change, Copilot archive dependencies/rotation, unchanged-file suppression, project/tag labels and cancellation. Codex messages retain one identity when their canonical projection arrives later.
- The current database schema directly includes import ordering. Removed obsolete receipt timestamps and compatibility migrations; old database layouts are not supported.
- Go race tests, vet, build and parser checks pass. `scripts/watch_test.py` verifies real local/HTTP watcher processes, offline retry, configuration paths, restart deduplication and graceful shutdown. The import/browser suite and Docker Compose startup, authentication and persistence checks also pass. Historical release validations follow below.

# v0.3.0 validation

- Added a local `agentlog import` command with repeatable folder/file selection, default-root scanning, working-directory filtering, project/tag labels and dry-run parsing. Supported transcript records are converted into OTLP logs and sent through authenticated HTTP ingestion.
- Tests cover Codex canonical messages vs duplicate event messages, Claude content/tool/usage blocks and subagent files, Copilot message/tool events and linked journals, stable replay IDs, metadata relabeling, updated records, exact integer usage, working-directory filtering, malformed input and unsupported linked Codex histories. Import HTTP redirects do not forward the access token.
- Added Resource attribute project display/search and per-signal query defaults. Explicit resource values take precedence. Tests cover logs/traces/metrics and ambiguous query rejection.
- Go race tests, vet and parser checks passed. The CLI/browser integration test exercises folder import, replay, native OTEL labels, shared sessions, origin filters, typed message/usage rendering, project search, inspector and mobile. The 206-session pagination/hostile-text browser suite also passed. Fixtures are synthetic; transcript version coverage remains limited to the documented shapes.
- Imported records have stable IDs and sequence ordering; existing databases receive an additive ordering column. Imported and live records are not heuristically deduplicated against each other. A dedicated source selector separates them.

# v0.2.1 validation

- Removed automatic event expiry, its startup/hourly pruning, CLI flag, and Compose/environment setting. Stored events now persist until explicit database/volume removal.
- The environment template requires only the access token. The optional port is commented with its default; the container version is no longer an environment setting.
- Database compatibility tests reopen a legacy-shaped database with year-old receipt timestamps and confirm all 205 events survive. The legacy receipt column remains for database compatibility; its unused expiry index is removed without deleting events.

# v0.2.0 validation

This version includes the revised UI and Docker Compose deployment. The v0.1.0 validation record is retained below.

- Replaced the split-pane dashboard with separate session-list and bookmarkable session-detail pages. Monochrome UI with a blue accent and semantic status colors; no custom icons, dashboard counters or promotional copy.
- Added schema-aware decoding for Claude prompt/response and tool attributes, Codex prompt/tool-result/usage events, and Copilot GenAI messages and tool spans. Commands, files, outputs, errors, timing and usage render on cards and in the event inspector before its JSON tab. No session token totals are inferred or summed.
- Metadata/parent/child links load independently from list pagination and search. Search now includes received content and retains full-session counts. Event-type filters are explicitly scoped to the displayed event page.
- Go tests (including new metadata authentication, search and encoded deep-link tests), race tests, vet and build passed. `node scripts/parse_test.cjs` verifies the three agent schemas, zero/absent values, metric temporality, error handling and Copilot billing multiplier semantics.
- Updated both Chromium test scripts for the new routes. Verified direct load/reload, list/search, parent and subagent navigation, structured content, export, inspector selection over polling, polling pause and failure recovery, 206-session pagination, 5KB hostile content, live ingestion and 390px layout.
- Density refinement: timestamps share event headings, the dedicated time gutter is removed, page/header/row/code spacing is reduced, and failed/succeeded states use labeled red/green styling. With the same synthetic data, full-page detail captures reduced from 1619 to 1123px on desktop and 1837 to 1242px on mobile. Browser assertions verify inline times, status labels/colors, left alignment and mobile overflow. Both browser suites pass.
- Event inspector revision: selectable event cards open a right-side Details/JSON panel, with structured properties, attributes, JSON copying and parent-span navigation. Selection and tab survive polling; filters and paging close a hidden selection. Enter/Space open a card, Escape closes and restores focus, tabs support arrow keys. At widths up to 900px the inspector is a full-screen dialog with background inert and trapped focus. Chromium tests cover these behaviors and mobile viewport bounds.
- Docker Compose was built and run against the managed local Docker daemon. `scripts/docker_smoke.py` verified non-root/read-only startup, authenticated HTTP ingestion, volume persistence across forced container recreation, and replay deduplication, then removed its isolated test project/volume. Release archives include Dockerfile, Compose settings, an empty-token environment template, build sources and scripts; checksums and all six cross-builds passed locally.
- Current screenshots: `ui-sessions.png`, `ui-claude.png`, `ui-codex.png`, `ui-copilot.png`, `ui-mobile.png`, `ui-event-details.png`, `ui-event-json.png`, `ui-event-mobile.png`. They use synthetic, schema-shaped HTTP fixtures from `scripts/demo.py`, not real CLI executions.

The remaining notes describe validation of the previously published version.

# v0.1.0 validation

Validated in the managed Linux amd64 development environment with Go 1.27.4, Python Playwright and system Chromium on 2026-11-13. All telemetry used for tests and screenshots is synthetic; no authenticated Codex, Claude or Copilot CLI session was run.

- Go tests: three source names; logs, trace spans, metrics; JSON hex trace/span IDs; gzip Protobuf; duplicate replay; child-before-parent relationships; cross-batch trace ownership; cumulative metrics retained as individual samples; authentication; signed viewing cookie; cookie refused for ingestion; JSON export; invalid payloads; oversize record batches rejected atomically; SQLite persistence, ordering (including fractional seconds), pagination and retention.
- `go test -race ./...`, `go vet ./...`, `go build ./cmd/agentlog`.
- Six successful pure-Go cross-builds: Linux, macOS and Windows, each amd64 and arm64. Archives include README, license and screenshots/validation notes; checksums are generated with SHA-256. Only Linux amd64 was executed locally. Other targets were cross-compiled, not run on their native operating systems.
- `scripts/demo.py` submitted 55 events into 10 sessions using real HTTP OTLP endpoints. `scripts/browser_test.py` exercised login failure/success, source filtering, search, empty results, parent/child navigation, JSON download, open raw-record state over automatic refresh, backend error/recovery and viewport overflow. Captured actual Chromium screenshots at 1440px desktop and 390px mobile widths.
- `scripts/browser_edge_test.py` starts an isolated collector, sends 410 events into 206 sessions, verifies both kinds of pagination, renders 5 KB of hostile HTML as plain text, checks the 390px layout, and observes an additional event arriving on the second timeline page via polling. No JavaScript page errors occurred.

Reproduce screenshots with an empty demo database and the same token in the server and scripts:

```sh
go build -o agentlog ./cmd/agentlog
export AGENTLOG_TOKEN=local-demo-token-only
./agentlog --db /tmp/fresh-demo.db
# In a second shell with AGENTLOG_TOKEN set to the same value:
python3 scripts/demo.py
python3 scripts/browser_test.py
python3 scripts/browser_edge_test.py
```

CLI setup examples were checked against the current official configuration references linked in README. Emitted content, signal coverage and parent attributes depend on CLI version and privacy settings. The viewer cannot recover information a sender does not export. This is a small single-user collector, not a high-volume ingestion service.

## Skill evidence accounting (v0.22.0)

Reported invocations and confirmed reads are separate metrics. Tests cover repeated reads within one session, import/OTEL and input/output deduplication, late and reverse result arrival, mixed origins, agent-scoped and reused call IDs, failure/deletion updates, shell/JavaScript literal parsing, and resumable recovery from original payloads rather than clipped cards. Nested Copilot invocation reports remain indexed during the rebuild.

The release-baseline upgrade check recovers successful compound-shell and JavaScript reads while preserving event identities and raw payload fingerprints. Browser checks cover project/time/origin filters, distinct report/read/pending/failure labels, sort and URL state, event/JSON navigation, and desktop/mobile screenshots. Migration lookups use separate full-key indexes, avoiding a scan of every skill read in a long session.

## Agent API access (v0.23.0)

Settings creates, renames, reveals, copies and deletes recoverable read-only API keys. API tests cover owner cookies/tokens, invalid and revoked credentials, key persistence after reopening the DB, explicit-key scope with owner cookies and with local authentication disabled, case-insensitive Bearer schemes, and cross-origin management requests. Read keys cannot manage keys, log in as the owner, ingest OTLP or mutate archives.

`/api/readme` serves the embedded Markdown API reference. `/api/metrics` reports the actual running version, uptime, raw/logical event counts, raw bytes and queue state using existing metadata counters. Tests distinguish original/imported duplicates from logical entries, handle deletions and report unavailable historical counts as null.

All 327 Go tests, focused auth/API race checks, vet/static checks, published-baseline DB upgrade, optional-auth and search browser flows pass. The Settings browser check exercises real curl discovery, the placeholder-only command, clipboard controls, rename/reload/revocation and four viewport widths; desktop/mobile screenshots were inspected. Docker Compose non-root/read-only startup, persistence and lazy original access remain valid.
