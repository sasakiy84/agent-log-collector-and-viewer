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
