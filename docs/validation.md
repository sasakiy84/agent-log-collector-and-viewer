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
