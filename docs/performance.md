# Session-view performance

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

Session lists are ordered by latest activity. Timelines open at the latest 200 entries, displayed oldest to newest, with the newest entry visible at the bottom. The latest window uses a descending index read instead of skipping the full history. **Older** and **Newer** move between adjacent windows; **Latest** returns to the newest entries. Refreshing an older window preserves the reading position.
