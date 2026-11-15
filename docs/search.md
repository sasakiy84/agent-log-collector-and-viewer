# Full-text search

Use [Search filters](search-filters.md) for time ranges, multiple projects/agents/origins, event types, AND/OR/exclusions and on-demand analysis in Sessions and Skills.

Search Japanese or English from the session list. By default, spaces combine words with AND; the final word also matches prefixes. Examples: `認証`, `検索 エラー`, `ﾊﾟｽﾜｰﾄﾞ`, `backend timeout`.

The default configuration is built into every binary and Docker image:

- **SQLite FTS5**: `unicode61 remove_diacritics 2`, prefix indexes for lengths 2 and 3, and contentless-delete storage. Original log text is not duplicated in the search table.
- **Kagome + IPADIC**: Japanese search-mode segmentation with Kagome's standard Lucene-style base-form, part-of-speech and stop-word filters.
- **Unicode NFKC and lowercase normalization**: full/half-width text and composed/decomposed kana match consistently. Japanese words such as `検索`, `認証` and `猫` do not require three characters or a log scan.

Full decoded **string values** are indexed: prompts, replies, tool input/output, project labels, event names and other textual attributes. Nested JSON strings are decoded too. JSON keys, ID fields, numbers, booleans, binary values and OTLP nanosecond timestamps are excluded. OTLP `intValue` is numeric even though its protobuf JSON representation is a string. A value explicitly sent as `stringValue` remains text. Card truncation does not affect search. Results retain the full session's event count and recent-activity order. Words are matched within an event; punctuation alone and Japanese stop words do not produce results. Queries are treated as literal words, with a limit of 512 characters / 64 distinct terms.

Open **Search options** and use **Exact ID** to find an ID by exact value. Choose an ID field or leave it empty to search all ID fields. Presets include session, event, trace, span, call, message and agent IDs; any actual ID attribute name, such as `rpc.request_id`, can also be entered. IDs retain case, punctuation and full/half-width characters, with no morphological analysis or normalization. For session IDs, both the collector's `codex:…` form and the sender's original `session.id` / `conversation.id` are indexed. Imported and OTEL field aliases such as `tool_call_id` and `gen_ai.tool.call.id` are also available under `call.id`.

ID lookups use a separate SQLite B-tree index on `(value,key,session)`, not FTS or a raw-log scan. Full-text and ID filters can be combined with project selection. The API accepts `id_key` and `id_value`, for example `/api/sessions?id_key=trace.id&id_value=0123456789abcdef0123456789abcdef`. Values are exact matches, up to 4096 characters. Existing internal-session and subagent visibility rules still apply.

Ingestion and search-index updates commit together. Archived records are removed from the index and identical replays stay suppressed. Archive compaction incrementally merges deleted FTS postings before reclaiming SQLite pages.

Existing records are rebuilt once by a versioned, resumable background job (`search_strings.v1`) after HTTP startup. Each batch replaces the previous full-text terms and adds exact-ID entries in the same transaction; completed batches are not repeated on restart. Ingestion and browsing remain available during preparation. No SQLite extension download, dictionary installation, environment variable or Docker override is required.

[SQLite FTS5](https://www.sqlite.org/fts5.html) · [Kagome](https://github.com/ikawaha/kagome) · [Kagome's Japanese filters](https://pkg.go.dev/github.com/ikawaha/kagome/v2/filter/ja)

## Validation

With 150,000 synthetic records across 500 sessions, an indexed Japanese keyword matching one session took 1.1 ms median (previous raw search: 315 ms). A common word matching every event took 114 ms; unfiltered list/detail reads remained around 2/4 ms. Initial view-plus-search indexing took 89 seconds in that run. These are local measurements, not deployment guarantees. A separate 3 MiB unpunctuated Japanese fixture indexed through its final word with about 230 MiB peak RSS under default runtime settings.

```sh
python3 scripts/performance_check.py --binary ./agentlog --japanese --payload-bytes 256
```

Japanese IME, decoded strings, exact-ID filters, reload and mobile layout: `scripts/search_browser_test.py`. Linux peak-memory regression: `scripts/search_memory_test.py`. Both use isolated synthetic data.
