# Storage and development

The service uses Go and SQLite. HTTP payloads and UI behavior are independent of table names. Original JSON bytes and public event/session IDs remain stable.

## Storage generation 2

`internal/app/schema.sql` is the complete, immutable generation-2 baseline, shared by the Go binary and the standalone Python converter. The main database identifies itself with `application_id=0x414C4732` and `user_version=1`. `schema_migrations` records each SQL migration’s SHA-256 and application time. `performance.sql` adds compact identifier postings and raw hashes, and removes retired repair indexes/columns. Future changes append entries to `migrations()`; do not edit a shipped baseline. DDL and history commit together, and missing/edited/future history is rejected.

The service does not adopt the old schema. Convert a stopped v0.23.0 data directory using [the Python migration](storage-migration.md). The converter transfers original and prepared data, SQLite indexes, pending accepted ingestion, watch checkpoints and archive files. A migrated database is immediately usable; startup does not rerun historical extraction jobs.

The main database has 41 application tables, including FTS5 and R-tree virtual tables but excluding SQLite shadow tables. Twelve typed, read-only `pending_*` views expose the single durable `work_items(kind,scope,key)` table. They do not add duplicated queue storage. The views allow each projection to name its own fields while queue writes have one storage owner.

## Write and read boundaries

- `store.go` preflights stable IDs and raw hashes on the read pool, rechecks under the writer reservation, and skips unchanged/tombstoned originals.
- `statements.go` reuses fixed statements for one ingestion transaction; no process-wide SQL cache is used.
- `analysis.go` interprets changed records and computes correlation keys and full-text terms before acquiring the single SQLite writer. Analyzer output does not depend on database state or localized display labels.
- `store.go` checks deletion tombstones and stable import identities, persists changed originals, facts and search documents, then invokes `refreshProjections` before committing.
- `projection.go` defines dependency order and attaches stage names to failures. Every ordinary ingestion batch publishes its related views atomically. Trace reassignment and archive deletion share this projection path.
- `record_batch.go` bounds original payloads loaded for explicit pending repairs. There are no old-format backfill jobs in the service.
- `records` contains original OTEL-shaped JSON. `record_facts` caches per-origin parsed presentations and indexed classifications. `logical_events` links imported and OTEL counterparts conservatively. Do not collapse per-origin evidence into merged state.
- Identifier strings and original keys/aliases live once in `identifier_values`; covering `identifier_postings` use the existing explicit search-document integer ID. Last-reference deletion prunes the dictionary locally.
- `sessions` stores project identity separately from its UI preview. Project and directory summaries remain incremental so sidebar/list requests do not decode whole histories.
- Session skill summaries reuse at most 64 revision/origin-scoped entries of up to 200 summaries; faceted Skills searches still use exact evidence queries.
- `session_usage` keeps accounting basis, coverage and exact decimal strings. Logs, traces, cumulative metrics and overlapping delta intervals cannot simply be summed.
- SQL triggers preserve transaction-local invalidations, counters, foreign-key cleanup and archive guards. No-op updates are gated where dependencies are unchanged. Do not bypass these invariants in normal application writes.

`service.go` opens the independent durable inbox, initializes storage/read pools, recovers only persisted invalidations and starts FIFO consumption. An acknowledged inbox batch survives a crash; a crash after publication but before queue deletion replays stable IDs without duplicate data. Watch checkpoints live in the inbox database.

WAL readers use read-only connections separate from the writer and export pool. Foreign keys, busy timeout and transaction settings are connection properties, including replacement connections. Export snapshots are concurrency-limited. Archive deletion records tombstones, checks user confirmation and proceeds in batches; native incremental vacuum reclaims pages without rebuilding history.

Useful read-only diagnostics:

```sql
SELECT version,name,applied_at FROM schema_migrations ORDER BY version;
SELECT kind,COUNT(*) FROM work_items GROUP BY kind;
SELECT SUM(records),SUM(raw_bytes) FROM record_totals;
```

The inbox has a separate schema history. Inspect `ingest_size`, `ingest_batches`, `watch_files` and `watch_records` in `agentlog.db.ingest.db`.

## Validation

```sh
go test ./...
go vet ./...
bash scripts/check-static.sh
uv run scripts/migrate_db_test.py
```

The Python tests need no Go toolchain. They use a frozen v0.23.0 synthetic fixture and cover raw bytes, committed WAL, FTS positions, interval row IDs, API keys, tombstones, queue/watch state, archives and failed publication. Compare a published v0.23.0 binary with the current binary through the actual APIs:

```sh
uv run --python '>=3.14' scripts/storage_migration_api_test.py \
  --baseline /path/to/v0.23.0/agentlog --binary ./agentlog
```

Performance fixtures separate materialized read performance from real ingestion. `frontend_performance_test.py --real-api` checks initial DOM, lazy requests and append work for 2k/20k/277k events. `session_performance_test.py --records 650962 --skewed --browser` includes uneven processing ranges. The optional `--baseline` on the latter compares two generation-2 binaries; use the migration API harness for cross-generation behavior checks. Docker smoke's scale mode ingests through OTLP rather than constructing a legacy raw-only database.

Run checks appropriate to the changed risks. There is no CI-artifact requirement for merging or releasing. PR titles and descriptions are Japanese; merge through a PR and create release tags from merged commits. The Release workflow packages `v*` tags; it does not deploy the production server.

[Additional performance review and removal audit](storage-performance-review.md).
