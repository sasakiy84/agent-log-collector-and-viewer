# Database lifecycle refactor review

This records the v0.9.1 refactor. The CI-proof release mechanism described here was subsequently removed in favor of local validation; see [the current development workflow](development.md#local-checks-and-releases).

Scope: maintain existing features and UI while making schema updates, resumable data maintenance and delivery checks explicit. Two focused reviewers examined database lifecycle and release verification, followed by an independent final review. P1–P3 findings were requested; P1/P2 fixes were reviewed again and given regression coverage.

## Resolved P2 findings

| Finding | Resolution and evidence |
| --- | --- |
| Every startup joins all original events against cards and search documents. | Move discovery into an applied-once migration; normal startup drains durable queues. Upgrade/restart test checks unchanged completed history. |
| Folder backfill holds all session IDs and restarts from the beginning. | Bounded session-key batches use the shared atomic checkpoint runner. |
| Feature-local `CREATE IF NOT EXISTS` lacks ordered, verifiable schema history. | Embedded immutable SQL, checksum ledger, atomic application and rejection of changed/gapped/future history. Atomic failure, retry, adoption and concurrent application tests. |
| Each ingestion rechecks all pending traces, including unrelated history and replay. | Durable source/trace queue; indexed reconciliation only for changed identities. Same ownership rules and late-owner tests retained. |
| Queued reconciliation can hit archive locks during startup, blocking archive recovery. | Defer locked identities without dropping work; reconcile after unlock. Reviewer reproduced the failure before the fix, then confirmed the regression test passes. |
| Startup card repair can leave stale usage totals when the historical usage job is already complete. | Flush pending usage changes before serving HTTP. Reviewer reproduced the stale total before the fix and confirmed recovery afterwards. |
| Release can publish an arbitrary tag without proof of merged source/full CI, while duplicating a partial set of tests. | Require main ancestry and a successful actual-tree CI proof, then build the tag source. Offline trust-boundary tests and live GitHub proof validation cover delivery. |

The startup path also clears changed timeline representatives before rebuilding destination sessions, avoiding view-ID collisions when a moved event's destination sorts before its previous session. Repeated connection settings now survive connection replacement. The v0.9.0 baseline schema was compared against the published binary: all 99 table/index/trigger definitions match after whitespace normalization.

## P3 findings

- Resolved: duplicate JavaScript check lists; all static JavaScript is now discovered by one script.
- Resolved: failed release builds leave staging directories; an EXIT trap cleans them up.
- Resolved: malformed/non-object proof JSON and unexpected ZIP contents; bounded parsing rejects these without executing downloaded code.
- Deferred: startup uses `context.Background()` before CLI signal-context registration. Transaction rollback and durable checkpoints protect interrupted updates, but cooperative startup cancellation is not yet exposed.
- Deferred: local packaging can leave archives for other versions in `dist/`. Actions uses a fresh checkout and is unaffected; each build replaces artifacts for its own version.

Final review results and release validation are recorded in the pull request.
