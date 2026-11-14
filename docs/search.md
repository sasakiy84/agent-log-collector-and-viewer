# Full-text search

Search Japanese or English from the session list. Spaces combine words with AND; the final word also matches prefixes. Examples: `認証`, `検索 エラー`, `ﾊﾟｽﾜｰﾄﾞ`, `backend timeout`.

The default configuration is built into every binary and Docker image:

- **SQLite FTS5**: `unicode61 remove_diacritics 2`, prefix indexes for lengths 2 and 3, and contentless-delete storage. Original log text is not duplicated in the search table.
- **Kagome + IPADIC**: Japanese search-mode segmentation with Kagome's standard Lucene-style base-form, part-of-speech and stop-word filters.
- **Unicode NFKC and lowercase normalization**: full/half-width text and composed/decomposed kana match consistently. Japanese words such as `検索`, `認証` and `猫` do not require three characters or a log scan.

Full decoded log content, JSON keys/values, project labels and session metadata are indexed. Card truncation does not affect search. Results retain the full session's event count and recent-activity order. Words are matched within an event; punctuation alone and Japanese stop words do not produce results. Queries are treated as literal words, with a limit of 512 characters / 64 distinct terms.

Ingestion and search-index updates commit together. Archived records are removed from the index and identical replays stay suppressed. Archive compaction incrementally merges deleted FTS postings before reclaiming SQLite pages.

Existing records are indexed once at startup in resumable batches. Allow startup time and disk space for the index. No SQLite extension download, dictionary installation, environment variable or Docker override is required.

[SQLite FTS5](https://www.sqlite.org/fts5.html) · [Kagome](https://github.com/ikawaha/kagome) · [Kagome's Japanese filters](https://pkg.go.dev/github.com/ikawaha/kagome/v2/filter/ja)

## Validation

With 150,000 synthetic records across 500 sessions, an indexed Japanese keyword matching one session took 1.1 ms median (previous raw search: 315 ms). A common word matching every event took 114 ms; unfiltered list/detail reads remained around 2/4 ms. Initial view-plus-search indexing took 89 seconds in that run. These are local measurements, not deployment guarantees. A separate 3 MiB unpunctuated Japanese fixture indexed through its final word with about 230 MiB peak RSS under default runtime settings.

```sh
python3 scripts/performance_check.py --binary ./agentlog --japanese --payload-bytes 256
```

Japanese IME, decoded JSON, filters and mobile layout: `scripts/search_browser_test.py`. Linux peak-memory regression: `scripts/search_memory_test.py`. Both use isolated synthetic data.
