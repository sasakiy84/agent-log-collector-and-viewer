# Import and watch sessions

[Docker Compose with mounted session folders](quickstart.md).

## Local collector

[agentlog.json example](examples/agentlog.watch.json):

```json
{
  "watch": [
    { "dir": "~/.codex/sessions" },
    { "dir": "~/.codex/archived_sessions" },
    { "dir": "~/.claude/projects" },
    { "dir": "~/.copilot/session-state" }
  ]
}
```

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog --config agentlog.json
```

Open http://127.0.0.1:4318. Imported records use the same OTLP normalization and SQLite storage as live telemetry. No CLI telemetry configuration is needed.

Optional fields (`watch_interval` defaults to `1s`):

```json
{
  "watch_interval": "2s",
  "watch": [
    {
      "dir": "~/.claude/projects",
      "source": "claude",
      "cwd": "/work/shop",
      "project": "shop",
      "tags": ["backend", "go"]
    }
  ]
}
```

`dir` is recursive. `source` defaults to `auto`. `cwd` filters recorded working directories, including descendants. `project` defaults to the recorded working-directory name; `tags` sets `project.tags`. Paths support `~/`; relative paths resolve from the configuration file. Restart after changing configuration. Avoid overlapping entries with conflicting project labels.

## Send to an existing collector

Run the watcher on the machine holding the files; the collector can run in Docker or on another host.

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog import --watch --all --endpoint http://127.0.0.1:4318
```

Omit `--watch` for a one-time import:

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
| `--watch` | Import existing files, then continuously import changed and new files |
| `--interval` | Polling interval for `--watch`; default `1s` |
| `--dry-run` | Parse and report without sending; token not required |

To select one project from a shared history directory:

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog import --all \
  --cwd /work/shop --project shop --tag backend \
  --endpoint https://agents.example.com
```

`--all` default locations honor `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and `COPILOT_HOME`:

| CLI | Default folders |
| --- | --- |
| Codex | `~/.codex/sessions/`, `~/.codex/archived_sessions/` |
| Claude | `~/.claude/projects/` |
| Copilot | `~/.copilot/session-state/` |

## Import behavior

- Watching starts with existing files, then reparses only changed files. Missing folders are watched once created. Failed imports retry; unfinished final JSON lines are reconsidered on the next change. `Ctrl+C` stops watching. `--watch` and `--dry-run` cannot be combined.
- Watching does not delete stored events when files are removed or truncated. Restarting safely replays existing files; no offset/state file is needed. Do not also enable native OTEL content export for the same sessions unless you want both origins.
- Converts messages, tool arguments/results and recorded usage into GenAI-style OTLP log attributes. JSON inspection shows the converted OTLP record. Unsupported record kinds are counted as skipped.
- Retains recorded session IDs and timestamps; explicit parent-session links are reused. Missing timestamps are marked, and unrecorded spans/durations are not fabricated. Attachments and encrypted reasoning are not imported.
- Re-running updates matching imported records without duplicating them. Appended records are added; changing project/tags updates their imported metadata. Import and live OTEL records can share a session; the source selector separates them. Cross-source deduplication is not attempted.
- Files are read to their size at import start. An unfinished final JSON line is skipped. Invalid interior records, oversized lines and missing predecessor files produce a nonzero exit; earlier successful batches remain stored and can be retried safely.
- Copilot linked journals are followed within the session directory; replay checkpoint prefixes are excluded. Missing/cyclic links fail explicitly.
- Codex `history_base` chains require a flattened transcript and currently fail explicitly. CLI transcript formats are version-dependent; this importer is validated with synthetic schema-shaped fixtures, not authenticated real sessions.
