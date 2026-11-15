# Maintenance review

This records the v0.13.0 review. CI-proof release verification was subsequently removed in favor of local checks; see [the current development workflow](development.md#local-checks-and-releases).

Scope: CLI/configuration, import/watch parsers, OTLP ingestion, database lifecycle,
migrations and projections, query/cache paths, archive stages, browser scripts,
delivery scripts and tests. Review passes covered ownership/duplication first,
then cancellation, transaction boundaries, cache dimensions and late telemetry.
Features, public HTTP responses and visual design are preserved except for the
incorrect results described below.

## Findings and disposition

No P1 finding was identified. All identified P2 findings are addressed.

| Priority | Finding | Resolution |
| --- | --- | --- |
| P2 | Startup registered termination signals after preparation; ingestion detached database work from canceled HTTP/watch requests. App lifetime was created by the archive subsystem. | One caller-owned context starts before database preparation and reaches startup, workers, HTTP base contexts, local sinks and write transactions. Open failure and normal shutdown share `Close`. |
| P2 | The folder summary cache ignored the project filter, including the meaningful empty/unassigned value. | One parsed filter supplies SQL and cache eligibility. Unsupported cache dimensions use the existing filtered query. |
| P2 | JSON OTLP reception rounded numeric int64 timestamps and counters through float64 before protobuf validation. | A strict, number-preserving JSON decoder serves telemetry documents, embedded values and JSON reception. |
| P2 | Resolved per-call usage retained an old ancestor after a closer/conflicting request or missing bridge changed the trace. | Trace changes coalesce in a durable queue; indexed reconciliation reevaluates affected usage with the existing unique-nearest/session/origin rules. Replace the old pending-only index with one matching trace/role/span lookups. Migration 0009 schedules existing facts once; previous migrations remain immutable. |
| P2 | Incremental archive reclamation followed a moving global freelist, allowing unrelated writes to extend a job indefinitely. | Bound reclamation by the initial free-page budget and count actual reclaimed pages, with ingestion allowed between batches. |
| P3 | Archive scheduling/export/deletion/reclamation shared one large implementation; failure handling duplicated nested transaction/rollback logic. | Separate stage files; scheduling owns recovery and uses the existing transaction runner. Export/delete bodies retain their previous behavior. |
| P3 | Repeated HTML shell handlers and failure-path cleanup obscured shared ownership. | Single shell handler and application cleanup path. |
| P3 | Compressed browser scripts made review difficult; an unused folder URL helper and always-zero project offset remained. | Format scripts, remove dead helper/state. The formatting step was checked by comparing Babel syntax trees, excluding source locations, comments and empty statements. |
| P3 | Browser features still communicate through shared globals. | Deferred: converting every feature to modules changes initialization/state ownership across all flows. Readable scripts and current navigation coverage provide an incremental starting point. |
| P3 | Standalone Python integration scripts repeat process/fixture setup. | Deferred: those fixtures cover different published-version and performance scenarios. A broad harness rewrite adds little failure coverage for this change. |

## Test value versus cost

- Add focused cancellation tests: canceled startup, a canceled receiver blocked on
  the single writer connection, and application workers stopping with their owner.
  They cover shutdown hangs without large fixtures or fixed sleeps.
- Extend the existing cached/dynamic folder comparison with project, unassigned
  and combined source/project filters. Avoid one test per implementation field.
- Exercise numeric int64 through the actual HTTP/protobuf pipeline for all three
  signals. Retain invalid/trailing JSON rejection.
- Exercise late closer requests, ambiguity and bridge removal. An older-schema
  fixture verifies repair of already-resolved facts and no repeated upgrade work.
- Retain archive download/confirmation/replay, corrupt archive, crash/resume,
  actual space reclamation and vacuum row-draining tests: failures can lose data
  or leave a large database unreclaimed. File moves need no extra unit tests.
- Retain published-binary upgrades, Japanese search, accounting precision and
  bounded-memory/performance checks. Their fixtures address previously observed
  production-scale failures. Avoid adding timing-sensitive producer stress tests
  or assertions that merely duplicate the new control flow.
- Browser checks cover project/activity navigation and the complete archive
  flow. Formatting alone is checked structurally rather than with new snapshots.

Validation results and CI links are recorded in the pull request.
