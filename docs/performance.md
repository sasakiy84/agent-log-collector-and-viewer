# Performance validation

The storage-generation-2 runtime starts from materialized records/facts/logical events and exact summaries. It does not interpret old schema layouts or rebuild all original histories on startup. Convert the stopped v0.23.0 directory with [the Python tool](storage-migration.md) before using this runtime.

## Current measurements

Synthetic local development measurements, with the current Go binary and headless Chromium. These are not production latency guarantees or percentage improvements over the old schema.

| Workload | Result |
|---|---|
| 650,962 records, most processing logs between the final messages | Latest conversation API first 37.5ms, subsequent median 9.31ms |
| Same skewed session | Browser initial display 312ms |
| 277,022 records with the real API | Session HTTP approximately 106ms, response-to-frames approximately 110ms |
| Same 200-card display, advertised totals 2k/20k/277k | 4,225 DOM nodes in each case; total history does not expand initial DOM |
| 20 explicitly expanded processing pages | 1,000 cards bound in batches of 50; old cards are not reinitialized |

The initial conversation request contains at most one page and bounded parsed previews, never every original JSON. Processing details fetch only when expanded. Full original data loads when inspected or exported. Minute counters and exact indexed edge ranges avoid traversing every collapsed log on initial conversation reads.

The session list uses materialized session previews and visible-root project counts. Project sidebar queries are independent of main-result rendering and errors. Full-text searches use FTS postings; identifiers use exact indexes, without morphological analysis. Per-session skill summaries use a set query rather than one query per skill.

## Reproduce

Build the current binary, then choose the checks relevant to a change:

```sh
go build -o agentlog ./cmd/agentlog

python3 scripts/session_performance_test.py \
  --binary ./agentlog --records 650962 --skewed --browser

python3 scripts/frontend_performance_test.py \
  --binary ./agentlog --real-api

python3 scripts/navigation_performance_test.py \
  --binary ./agentlog --records 650962 --sessions 13085 --browser

python3 scripts/overview_performance_test.py \
  --binary ./agentlog --sessions 20000 --browser

AGENTLOG_BIN=./agentlog python3 scripts/usage_performance_test.py
AGENTLOG_BIN=./agentlog python3 scripts/startup_ingestion_test.py
```

Read-performance fixtures insert prepared synthetic rows and their summaries to isolate SQL/rendering costs. They are not an ingestion benchmark. The usage benchmark sends real reverse-ordered delta metrics through HTTP. The startup test exercises accepted ingestion with the main writer blocked, SIGKILL/restart, persistent watch checkpoints and concurrent reads. Docker smoke's optional scale mode also uses real HTTP ingestion.

The optional baseline arguments on materialized fixture benchmarks compare generation-2 binaries. Cross-generation compatibility is tested separately by converting a real v0.23.0 database and comparing 60 API responses:

```sh
uv run --python '>=3.14' scripts/storage_migration_api_test.py \
  --baseline /path/to/v0.23.0/agentlog --binary ./agentlog
```

Measurements need room for the synthetic DB, WAL and fixture copies. Offline conversion additionally validates the original and destination bytes and indexes; its resource use is separate from runtime performance. Current reviewed limitations are recorded in [storage-v2-review.md](storage-v2-review.md).
