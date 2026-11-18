# Sender configuration

Examples include conversation and tool content. [Claude/Copilot rendering and validation](provider-display.md).

## Codex CLI

`~/.codex/config.toml`

```toml
[otel]
log_user_prompt = true
log_agent_responses = true
exporter = { otlp-http = { endpoint = "https://agents.example.com/v1/logs", protocol = "binary", headers = { "Authorization" = "Bearer REPLACE_WITH_TOKEN" } } }
trace_exporter = { otlp-http = { endpoint = "https://agents.example.com/v1/traces", protocol = "binary", headers = { "Authorization" = "Bearer REPLACE_WITH_TOKEN" } } }
```

Optional project `shop`: use these URLs for `exporter` and `trace_exporter`, respectively:

```text
https://agents.example.com/v1/logs?project=shop
https://agents.example.com/v1/traces?project=shop
```

## Claude Code CLI

`~/.claude/settings.json` · Windows: `%USERPROFILE%\.claude\settings.json`

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_METRICS_EXPORTER": "otlp",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "https://agents.example.com",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer REPLACE_WITH_TOKEN",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_LOG_USER_PROMPTS": "1",
    "OTEL_LOG_ASSISTANT_RESPONSES": "1",
    "OTEL_LOG_TOOL_DETAILS": "1",
    "OTEL_LOG_TOOL_CONTENT": "1"
  }
}
```

Optional project `shop`: replace `OTEL_EXPORTER_OTLP_ENDPOINT` with these values in `env`:

```json
{
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "https://agents.example.com/v1/logs?project=shop",
  "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://agents.example.com/v1/traces?project=shop",
  "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://agents.example.com/v1/metrics?project=shop"
}
```

## GitHub Copilot CLI

| OS | Path |
| --- | --- |
| Linux | `/etc/github-copilot/managed-settings.json` |
| macOS | `/Library/Application Support/GitHubCopilot/managed-settings.json` |
| Windows | `%ProgramFiles%\GitHubCopilot\managed-settings.json` |

Machine-wide managed settings; administrator access required. On macOS/Linux: root-owned, no symlink, not world-writable, readable by the CLI user.

```json
{
  "telemetry": {
    "enabled": true,
    "endpoint": "https://agents.example.com",
    "protocol": "http/protobuf",
    "headers": {
      "Authorization": "Bearer REPLACE_WITH_TOKEN"
    },
    "capture": {
      "prompts": true,
      "responses": true,
      "toolArguments": true,
      "toolOutput": true
    }
  }
}
```

Optional project `shop`: add to `telemetry` (keep `endpoint` as the base URL):

```json
{
  "resourceAttributes": {
    "service.namespace": "shop"
  }
}
```

`project` query parameters supply `service.namespace` only when the resource has neither `service.namespace` nor `project.name`. Fixed project settings apply to all sessions using that configuration. [Project tags and resource attributes](projects.md).

[Codex reference](https://developers.openai.com/codex/config-reference) · [Claude reference](https://code.claude.com/docs/en/monitoring-usage) · [Copilot reference](https://docs.github.com/en/copilot/reference/enterprise-administrators/enterprise-managed-settings#telemetry)
