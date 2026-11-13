# Agentlog

A personal OpenTelemetry collector and session viewer for Codex CLI, Claude Code and GitHub Copilot CLI.

![Session timeline and event inspector](docs/ui-event-details.png)

*Screenshot uses synthetic telemetry.*

## Features

- Collect logs, traces and metrics over OTLP/HTTP (JSON or Protobuf).
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

### 2. Set up HTTPS for remote access

Keep port 4318 on loopback and put an HTTPS reverse proxy in front. Example Caddy configuration on the same host:

```caddyfile
agents.example.com {
    reverse_proxy 127.0.0.1:4318
}
```

Use your own domain. Copilot requires HTTPS for OTLP export.

### 3. Configure your CLI

Merge the linked example into the indicated configuration file, replace the endpoint and token, then restart the CLI. Use the same token as Agentlog.

| CLI | Configuration file | Example |
| --- | --- | --- |
| Codex | `~/.codex/config.toml` | [TOML](docs/examples/codex.config.toml) |
| Claude Code | `~/.claude/settings.json` (`env`) | [JSON](docs/examples/claude.settings.json) |
| Copilot | OS-specific `managed-settings.json` (`telemetry`) | [JSON](docs/examples/copilot.managed-settings.json) |

Copilot managed settings require administrator access and apply machine-wide. See [file locations and content-capture settings](docs/senders.md). Claude's exporter settings belong in the user-level file, not a repository-local file. Prompt and output capture is opt-in; the guide lists the settings.
