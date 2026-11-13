# Import saved sessions

Run `agentlog import` on the machine holding the session files. It converts supported content into OTLP/HTTP logs and sends it to the existing collector.

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog import \
  --dir /path/to/session-files \
  --endpoint https://agents.example.com \
  --project shop --tag backend --tag go
```

| Argument | Value |
| --- | --- |
| `--dir` | Transcript folder; recursive; repeatable |
| `--file` | Individual JSONL file; repeatable |
| `--all` | Scan the three CLIs' default transcript directories |
| `--cwd` | Include only sessions whose recorded working directory matches this path or a descendant |
| `--source` | `auto` (default), `codex`, `claude`, `copilot` |
| `--endpoint` | Collector base URL or full `/v1/logs` URL |
| `--project` | `service.namespace`; otherwise derived from the recorded working-directory name |
| `--tag` | `project.tags`; repeatable |
| `--dry-run` | Parse and report without sending; token not required |

To select one project from a shared history directory:

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog import --all \
  --cwd /work/shop --project shop --tag backend \
  --endpoint https://agents.example.com
```

Default locations honor `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and `COPILOT_HOME`:

| CLI | Default folders |
| --- | --- |
| Codex | `~/.codex/sessions/`, `~/.codex/archived_sessions/` |
| Claude | `~/.claude/projects/` |
| Copilot | `~/.copilot/session-state/` |

- Converts messages, tool arguments/results and recorded usage into GenAI-style OTLP log attributes. JSON inspection shows the converted OTLP record. Unsupported record kinds are counted as skipped.
- Retains recorded session IDs and timestamps; explicit parent-session links are reused. Missing timestamps are marked, and unrecorded spans/durations are not fabricated. Attachments and encrypted reasoning are not imported.
- Re-running updates matching imported records without duplicating them. Appended records are added; changing project/tags updates their imported metadata. Import and live OTEL records can share a session; the source selector separates them. Cross-source deduplication is not attempted.
- Files are read to their size at import start. An unfinished final JSON line is skipped. Invalid interior records, oversized lines and missing predecessor files produce a nonzero exit; earlier successful batches remain stored and can be retried safely.
- Copilot linked journals are followed within the session directory; replay checkpoint prefixes are excluded. Missing/cyclic links fail explicitly.
- Codex `history_base` chains require a flattened transcript and currently fail explicitly. CLI transcript formats are version-dependent; this importer is validated with synthetic schema-shaped fixtures, not authenticated real sessions.
