# Session-view performance

The folder overview reads cached per-folder/source summaries instead of decoding and adding every session's usage on each GET. In a local synthetic fixture with 20,000 sessions, 20 folders and rich usage reports, warm HTTP medians were **1,472 ms → 1.63 ms** versus v0.10.2. Reusing browser number/date formatters reduced rendering 200 session rows from **57.9 ms → 8.3 ms**. Responses, counts, ordering and exact usage values matched the baseline. These measurements are fixture-specific, not deployment guarantees.

Folder membership, exact decimal totals, coverage and latest activity update in the ingestion transaction. Updates subtract the previous contribution before adding the replacement; archive locks remove contributions before deletion begins. Folder/source/diagnostic filters use the cached groups. Search and skill filters still aggregate their selected sessions. Initial upgrade populates the cache from session summaries in resumable batches of 200; it does not read original logs. The 20,000-session upgrade took 3.75 seconds in this run. Normal startup does not repeat completed work.

```sh
python3 scripts/overview_performance_test.py --binary ./agentlog --baseline /path/to/v0.10.2/agentlog --browser
```

Measured locally with 150,000 synthetic imported records, 500 sessions, and 2 KiB prompt payloads per record. Medians are three reads following the first request, on the same machine and fixture; they are not deployment guarantees.

| API | Before | After | Response before → after |
| --- | ---: | ---: | ---: |
| Session list | 3,423 ms | 2.6 ms | 897,467 → 116,180 bytes |
| Session detail | 1,256 ms | 14.2 ms | 1,418,489 → 526,383 bytes |

The first detail read after upgrade was 158 ms. The one-time upgrade took 56 seconds in this run. The benchmark checks identical session counts, ordering and event identities between binaries.

```sh
go build -o agentlog ./cmd/agentlog
python3 scripts/performance_check.py --binary ./agentlog --baseline /path/to/previous/agentlog
```

The benchmark creates isolated synthetic databases and removes them afterward. It needs several GiB of temporary disk space at the default size. `--records`, `--sessions` and `--payload-bytes` adjust the fixture.

Indexed session summaries and bounded card presentations are maintained in the same transaction as ingestion. Counts use delta updates; time boundaries use index endpoints. Mixed imported/OTEL updates recalculate only the affected shared-ID component, including former neighbors when identifiers change. Read-only WAL connections keep UI reads independent of the ingestion connection.

List/detail requests read their page from these indexes. Full attributes and original JSON load through `/api/event` only when a card is opened; the browser retains at most the latest full card. Search uses a separate FTS5 inverted index; only exports and explicit event inspection read original records. There are no periodic browser requests; **Refresh** reloads the current screen.

Existing databases build the display data once before accepting HTTP requests. Both card batches and completed sessions are committed separately so an interrupted upgrade resumes. These derived tables require additional disk space; original records remain intact.

Session lists are ordered by latest activity. Shared numbered pagination shows the current range and total, first/last pages, nearby pages with ellipses, and a validated page-number field. The compact bar stays visible while scrolling. Lists count only indexed session summaries; filtered totals and rows share one read transaction (`X-Total-Count` and `X-Page-Offset`). Stale offsets clamp to the last valid page.

Timelines use fixed 200-entry pages from the oldest event. They open on the last page, ordered oldest to newest, with the newest event visible at the bottom. The last page uses a descending index read bounded to its actual size, rather than skipping the history. **Latest** returns to that page. Historical page links retain their page/origin on reload; refreshing an older page preserves the reading position. Appending events does not shift previous page boundaries.

The default overview groups all matching sessions by their full working-directory path before pagination. Opening a folder fetches only that folder's session page. Folder totals, agent sets and latest activity come from indexed session summaries, including when search or an agent filter is applied. Missing directory metadata forms one **Folder unavailable** group; equal basenames at different paths remain separate.

A partial directory-candidate index supplies the earliest user event with known metadata, then falls back to other known events. Existing databases populate the summary directories once at startup; only legacy paths clipped at 512 bytes need one original-record lookup. Directory identities accept strings up to 32 KiB; larger values and other types appear under Folder unavailable and remain unchanged in original records. Folder reads never read original payloads.

```sh
python3 scripts/performance_check.py --binary ./agentlog --folders
```

Usage summaries are maintained incrementally from normalized reporting groups. With the same 150,000-record/500-session/20-folder synthetic fixture, warm medians after adding accounting were 2.90 ms for session lists, 7.55 ms for details and 2.82 ms for folders. Session counts, order and event identities matched v0.6.0. Reverse-order OTEL delta batches of 1k/2k/4k/10k records took 1.35/3.13/5.83/16.36 seconds including ingestion and full-text indexing; accounting uses an exact integer RTree rather than a suffix scan per interval. These local measurements are fixture-specific.

Subagent links use one batched lookup for visible cards and indexed lookups for actual launch calls. In a synthetic 10,000-tool-record history, warm HTTP medians across 12 reads were 6.93 ms for the session detail (v0.7.2: 7.78 ms) and 1.12 ms for a 45-event runtime-agent preview. The one-time subagent index upgrade took 0.51 s. Preview pages contain at most 40 cached cards and do not fetch original JSON.

Tool input/output lookup reuses phase-scoped call-ID keys and reads only a bounded counterpart card. Skill summaries read a separate evidence index; historical skill recovery runs in background batches after HTTP startup. With a 10k-tool Codex fixture, local warm medians were 0.67 ms for session lists and 8.78 ms for details (v0.8.0: 0.53 / 7.25 ms). The updated server began listening after 0.054 s against that existing database; skill history continued in the background.

Versioned migrations move missing-card/search discovery out of ordinary startup. With 10,000 unresolved Codex traces retained in identical ready databases, local median startup was 20.86 ms (v0.9.0: 41.89 ms); unrelated single-log ingestion was 3.42 ms (v0.9.0: 24.97 ms). The initial migration took 73.76 ms. These are warm, local synthetic measurements, not a large-production-DB estimate. `scripts/lifecycle_performance.py` reproduces this workload. Changed trace identities are queued transactionally, so unrelated appends no longer revisit all unresolved spans.
