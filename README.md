# Agentlog

A personal OpenTelemetry collector and session viewer for Codex CLI, Claude Code and GitHub Copilot CLI.

![Session timeline and event inspector](docs/ui-event-details.png)

*Screenshot uses synthetic telemetry.*

## Features

- Collect logs, traces and metrics over OTLP/HTTP (JSON or Protobuf), or import saved CLI sessions.
- Read session timelines, tool calls, outputs and errors; inspect attributes/JSON and export sessions.
- One Go binary with an embedded UI, SQLite storage and token authentication. No automatic log expiry.

## Setup

### 1. Start with Docker Compose

Requires Docker with Compose v2.

```sh
git clone https://github.com/sasakiy84/agent-log-collector-and-viewer.git
cd agent-log-collector-and-viewer
cp .env.example .env
openssl rand -hex 32
```

Set the generated value as `AGENTLOG_TOKEN` in `.env`, then start:

```sh
docker compose up -d --build
```

Open http://127.0.0.1:4318 and sign in with that token. SQLite data persists in a named volume across container recreation; `docker compose down --volumes` deletes it.

Without Docker, download a binary from [Releases](https://github.com/sasakiy84/agent-log-collector-and-viewer/releases) and run `AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog`.

### 2. HTTPS with Docker

Add your domain to `.env`:

```dotenv
AGENTLOG_DOMAIN=agents.example.com
```

Point its DNS records at the server and allow inbound TCP 80/443. Then:

```sh
docker compose -f compose.yaml -f compose.https.yaml up -d --build
```

Caddy obtains and renews the certificate automatically. Open `https://agents.example.com` and use that base URL in Copilot's `telemetry.endpoint`. Certificates persist in a Docker volume. [Localhost HTTPS](docs/https.md#localhost).

### 3. Configure your CLI

| CLI | Configuration file | Example |
| --- | --- | --- |
| Codex | `~/.codex/config.toml` | [TOML](docs/examples/codex.config.toml) |
| Claude Code | `~/.claude/settings.json` (`env`) | [JSON](docs/examples/claude.settings.json) |
| Copilot | OS-specific `managed-settings.json` (`telemetry`) | [JSON](docs/examples/copilot.managed-settings.json) |

Examples include conversation and tool content. [Paths and complete settings](docs/senders.md). Copilot managed settings require administrator access.

### 4. Import existing sessions

```sh
AGENTLOG_TOKEN=YOUR_TOKEN ./agentlog import --dir /path/to/sessions --endpoint https://agents.example.com --project shop --tag backend
```

[Import arguments](docs/import.md) · [Projects and tags for live OTEL](docs/projects.md)
