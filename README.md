# Agentlog

A personal OpenTelemetry collector and session viewer for Codex CLI, Claude Code and GitHub Copilot CLI.

![Project navigation and session search](docs/ui-projects.png)

*Screenshot uses synthetic telemetry.*

## Features

- Collect logs, traces and metrics over OTLP/HTTP (JSON or Protobuf), or import and watch saved CLI sessions.
- Search Japanese and English with built-in SQLite FTS5 and an embedded Japanese dictionary ([search settings](docs/search.md)).
- Keep projects visible beside session search; timelines open at the latest activity. Inspect [tool inputs/outputs](docs/tool-events.md) and [subagent logs](docs/subagents.md) in the side panel.
- Rank [skills](docs/skills.md) by usage across sessions or per project; search names, plugins and references, then open matching sessions.
- Compare reported tokens, cache usage, USD cost, request time and errors per session ([accounting rules](docs/usage.md)).
- Read conversations and tool [activity](docs/activity.md) first; inspect internal telemetry in Diagnostics.
- Combine imported and OTEL events with shared IDs into one card while keeping both originals.
- [Export and archive](docs/export.md) from the session list: download complete logs or archive inactive folders, confirm the download, then reclaim DB space.
- One Go binary with an embedded UI, SQLite storage and optional token authentication. No automatic log expiry.

## Setup

[QuickStart: Docker Compose + folder watching + host Nginx](docs/quickstart.md) (Ubuntu/Debian).

### 1. Start with Docker Compose

Requires Docker with Compose v2.

```sh
git clone https://github.com/sasakiy84/agent-log-collector-and-viewer.git
cd agent-log-collector-and-viewer
cp .env.example .env
docker compose up -d --build
```

Existing databases build session and full-text indexes once on first startup; the upgrade is logged and interrupted upgrades resume.

Open http://127.0.0.1:4318. Authentication is disabled when `AGENTLOG_TOKEN` is unset or empty. To require authentication, set a secret of at least 16 characters in `.env` and sign in with it. SQLite data persists in a named volume across container recreation; `docker compose down --volumes` deletes it.

Without Docker, download a binary from [Releases](https://github.com/sasakiy84/agent-log-collector-and-viewer/releases) and run `./agentlog`. Set the `AGENTLOG_TOKEN` environment variable to enable authentication.

### 2. Set up HTTPS for remote access

Keep port 4318 on loopback and put an HTTPS reverse proxy in front. [Nginx configuration](docs/quickstart.md#2-ホスト側のnginx). Copilot requires HTTPS for OTLP export.

### 3. Configure your CLI

| CLI | Configuration file | Example |
| --- | --- | --- |
| Codex | `~/.codex/config.toml` | [TOML](docs/examples/codex.config.toml) |
| Claude Code | `~/.claude/settings.json` (`env`) | [JSON](docs/examples/claude.settings.json) |
| Copilot | OS-specific `managed-settings.json` (`telemetry`) | [JSON](docs/examples/copilot.managed-settings.json) |

Examples include conversation and tool content. [Paths and complete settings](docs/senders.md). Copilot managed settings require administrator access.

### 4. Import or watch session folders

Local collection and viewer, without CLI telemetry configuration or HTTPS:

```sh
cp docs/examples/agentlog.watch.json agentlog.json
./agentlog --config agentlog.json
```

Send local files to an existing collector (including Docker):

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog import --watch --all --endpoint http://127.0.0.1:4318
```

[Archiving inactive sessions](docs/archive.md)

Omit `--watch` for a one-time import. [Folders, project tags and settings](docs/import.md) · [Projects and tags for live OTEL](docs/projects.md)
