# Database and delivery maintenance

The HTTP API, static UI, import identities and original event payloads are unchanged by the database lifecycle refactor.

## Schema changes

`internal/app/migrations/*.sql` is the schema history, embedded in the binary. Files apply in numeric order. `schema_migrations` records each filename, SHA-256 checksum and application time. Pending SQL and ledger entries commit atomically; failure rolls back the pending set. An edited migration, a gap or a newer schema is rejected instead of attempting a downgrade.

Add the next numbered SQL file for every schema change. Never edit a migration that has shipped. The baseline is the complete v0.9.0 schema; adoption preserves existing data and independently recorded usage, subagent and skill checkpoints. The old `PRAGMA user_version` was only a folder-index flag and is not interpreted as a schema version.

Missing cards/search documents are discovered once during adoption. Subsequent ingestion maintains durable dirty queues in the same transaction as the original event. Ordinary startup processes pending queues rather than auditing all original records. Direct manual edits to derived tables are unsupported; a repair should be an explicit migration or a versioned backfill.

## Data backfills

`maintenance_jobs` is independent of schema history. A job name includes its algorithm version (`usage.v1`, `agents.v1`, `skills.v1`, `directories.v1`, `titles.v1`). When the extraction logic changes, schedule a new name in a new migration. Do not reset a shipped checkpoint implicitly.

`runMaintenance` commits a bounded batch and its cursor together. Empty batches mark completion, non-advancing cursors fail, and errors retain the last committed checkpoint. The HTTP service opens its independent durable inbox first. Schema migrations then initialize the view database and read pool; historical jobs run in the background with pauses between committed batches. Historical skill recovery also remains a background job. Logs report progress. Restart resumes failed/incomplete work; the last error is retained until a successful batch.

For operational diagnosis, open the database read-only and inspect:

```sql
SELECT version,name,applied_at FROM schema_migrations ORDER BY version;
SELECT name,complete,processed,updated_at,error FROM maintenance_jobs ORDER BY name;
```

`refreshProjections` defines write dependency order: trace ownership, cards, full-text search, timelines, activity metadata, per-call statistics, and overview summaries. The background startup path commits resumable card/session batches and flushes usage totals before consuming the durable inbox. Readers can access committed views after schema initialization; archive operations wait for preparation, and archive creation/deletion also checks pending ingestion for the selected sessions, without blocking unrelated archives or recovery actions. Changed trace identities use `trace_dirty`; identities containing archive-locked sessions stay queued until the locks are released.

Archive listings join indexed session timestamps with `archive_stats` record/byte counters maintained by event triggers. `archive_stats_pending` is the resumable upgrade queue; no ordinary archive-list request reads original payloads. Eligibility uses the original per-origin timestamps, including unknown times hidden by imported/OTEL card pairing. Folder membership is frozen in the archive snapshot. Running-job updates use an authenticated SSE stream notified after committed state changes, without browser polling.

Database URI construction applies foreign keys, busy timeout and writer transaction settings to every connection, including replacements. WAL readers and archive snapshots use read-only connections.

## Validation

```sh
bash scripts/check-static.sh
go test -race ./...
go vet ./...
go build ./cmd/agentlog
bash scripts/test-upgrade.sh
```

The upgrade check downloads the Linux v0.9.0 release with a pinned SHA-256, builds a synthetic database with all three agents and archived data, and compares established API fields before/after upgrade and restart (allowing the new optional presentation metadata). It verifies unchanged original identities/payloads, foreign keys, archive replay suppression and unchanged completed checkpoints. To use an existing baseline binary:

```sh
AGENTLOG_BASELINE_BIN=/path/to/v0.9.0/agentlog bash scripts/test-upgrade.sh
```

The optional lifecycle benchmark compares identical ready databases containing unresolved internal traces:

```sh
python3 scripts/lifecycle_performance.py --binary ./agentlog --baseline /path/to/v0.9.0/agentlog
```

Use `AGENTLOG_BIN=./agentlog python3 scripts/startup_ingestion_test.py` for the real-process blocked-migration, ACK/SIGKILL/restart, checkpoint and concurrent-read contract. Integration scripts use `test_support.settled_urlopen` when they need derived results after uploads; queue latency/crash tests deliberately use raw urllib requests.

When transcript normalization changes, bump `watchParserVersion` so persistent unchanged-file checkpoints are reconsidered with the new parser.

Use the browser flows for affected features. `AGENTLOG_BIN=./agentlog python3 scripts/activity_projects_browser_test.py` checks project navigation and Activity/Diagnostics pagination on desktop and mobile.

## Local checks and releases

Run checks locally according to the changed behavior. The commands above cover Go/static checks and database upgrades. Use watcher, search, accounting, overview, browser or Docker scripts when the change affects those paths. There is no CI workflow or CI-artifact requirement for merging or releasing.

Write PR titles and descriptions in Japanese, include relevant local validation, and merge through the PR. Create release tags from merged commits.

Pushing a `v*` tag starts the Release workflow. It builds and packages the tag's source for the supported platforms and publishes the GitHub release. It does not run tests or look up CI runs or validation artifacts.

```sh
git tag -a vX.Y.Z MERGED_COMMIT -m 'Agentlog vX.Y.Z'
git push origin refs/tags/vX.Y.Z
```
