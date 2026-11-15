# Database and delivery maintenance

The HTTP API, static UI, import identities and original event payloads are unchanged by the database lifecycle refactor.

## Schema changes

`internal/app/migrations/*.sql` is the schema history, embedded in the binary. Files apply in numeric order. `schema_migrations` records each filename, SHA-256 checksum and application time. Pending SQL and ledger entries commit atomically; failure rolls back the pending set. An edited migration, a gap or a newer schema is rejected instead of attempting a downgrade.

Add the next numbered SQL file for every schema change. Never edit a migration that has shipped. The baseline is the complete v0.9.0 schema; adoption preserves existing data and independently recorded usage, subagent and skill checkpoints. The old `PRAGMA user_version` was only a folder-index flag and is not interpreted as a schema version.

Missing cards/search documents are discovered once during adoption. Subsequent ingestion maintains durable dirty queues in the same transaction as the original event. Ordinary startup processes pending queues rather than auditing all original records. Direct manual edits to derived tables are unsupported; a repair should be an explicit migration or a versioned backfill.

## Data backfills

`maintenance_jobs` is independent of schema history. A job name includes its algorithm version (`usage.v1`, `agents.v1`, `skills.v1`, `directories.v1`, `titles.v1`). When the extraction logic changes, schedule a new name in a new migration. Do not reset a shipped checkpoint implicitly.

`runMaintenance` commits a bounded batch and its cursor together. Empty batches mark completion, non-advancing cursors fail, and errors retain the last committed checkpoint. Required jobs finish before HTTP serves requests; historical skill recovery remains a background job. Logs report progress. Restart resumes failed/incomplete work; the last error is retained until a successful batch.

For operational diagnosis, open the database read-only and inspect:

```sql
SELECT version,name,applied_at FROM schema_migrations ORDER BY version;
SELECT name,complete,processed,updated_at,error FROM maintenance_jobs ORDER BY name;
```

`refreshProjections` defines write dependency order: trace ownership, cards, full-text search, timelines, activity metadata, per-call statistics, and overview summaries. The startup path commits resumable card/session batches and flushes usage totals before exposing the API. Changed trace identities use `trace_dirty`; identities containing archive-locked sessions stay queued until the locks are released.

Database URI construction applies foreign keys, busy timeout and writer transaction settings to every connection, including replacements. WAL readers and archive snapshots use read-only connections.

## Validation

```sh
bash scripts/check-static.sh
go test -race ./...
go vet ./...
python3 -m unittest discover -s scripts -p '*_unit_test.py'
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

Use the browser flows for affected features. `AGENTLOG_BIN=./agentlog python3 scripts/activity_projects_browser_test.py` checks project navigation and Activity/Diagnostics pagination on desktop and mobile.

## CI and releases

PR CI runs race/static checks, the published-database upgrade check, watcher/search/accounting checks, platform builds and Docker's large-history/archive check. A final `verified` job records the actual checkout commit and Git tree in a small JSON artifact after the test and Docker jobs succeed. All JavaScript files are checked automatically.

Release verifies that its commit is on `main` and has a matching proof from this repository's successful CI workflow. PR CI checks a synthetic merge commit, so its actual tested tree is compared rather than assuming the run's `head_sha` was checked out. The repository, head repository, workflow, required jobs, run attempt, artifact contents and Git commit tree are checked. Fork PR proofs are rejected; the merged commit's main CI supplies the proof instead.

Release then builds/packages from the tag's own source and publishes. It does not execute downloaded artifact code or rerun already successful tests. Proofs are retained for 30 days. If no matching proof remains, run CI on the tag and rerun Release:

```sh
gh workflow run ci.yml --ref vX.Y.Z
gh run rerun RELEASE_RUN_ID
```

The workflow also accepts that successful manual CI proof for the exact merged commit. Failed or mismatched proof validation stops publication.
