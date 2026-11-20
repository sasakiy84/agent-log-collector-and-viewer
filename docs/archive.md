# Archive

![Archive download and confirmation, using synthetic sessions](ui-archive.png)

1. In **Sessions**, open the menu beside **Export ZIP** and choose **Archive inactive sessions…**. The panel covers all projects; the list’s search stays unchanged. Folders containing sessions inactive for over one calendar month appear here. Sessions with unknown timestamps are excluded.
2. Select one or more folders, then **Create ZIP**. Only inactive sessions in those folders are included, across all agents. Use **Select all** to include every eligible folder across all pages. Uncheck any folders you want to exclude, or use **Clear** to reset the selection. Job progress is pushed while the operation runs.
3. **Download ZIP**, check the local file, then type **DOWNLOADED** and choose **Delete archived data**.

Downloading alone never deletes records. Updated sessions require a new archive. Canceling keeps DB records. After deletion, the database drops the selected session metadata and retains the job summary and replay fingerprints. Source files stay in place. Identical archived records are ignored on reimport, while new or changed records can be imported.

ZIP files contain `manifest.json`, a `sessions.jsonl` index, and one JSONL per session with all stored originals, including imported and OTEL records. These are AGrahpite records, not the CLI's native import format. The index preserves each session's selected folder. The UI shows a SHA-256 checksum.

Archives are built under `archives/` beside the database, streamed to disk, and downloaded directly with HTTP range support. Allow disk space for the compressed ZIP. The server copy is removed after all archived records have been deleted. Confirmed deletion interrupted by a restart resumes automatically. If the panel shows **Deletion paused**, resolve the cause shown in the server log and choose **Retry deletion** to resume the same job. The selection stays locked while deletion is paused. Interrupted ZIP creation must be retried.

New databases return free pages to disk automatically in small batches, allowing ingestion between batches. If compaction fails, freed pages remain reusable. When another database operation blocks WAL truncation, wait for it to finish and choose **Retry compaction**. For storage errors, free disk space before retrying.

Existing databases first keep freed pages available for reuse. To return this space to disk, choose **Rebuild database · pauses ingestion**. This one-time SQLite rebuild can require up to twice the database size in free space; clients should retry ingestion until it finishes. Subsequent archives use incremental reclamation.

Archive listings read indexed session summaries and incrementally maintained record/byte counts. They do not scan original event payloads. The offline migration preserves and verifies existing counts. No Refresh button or periodic browser polling is used.
