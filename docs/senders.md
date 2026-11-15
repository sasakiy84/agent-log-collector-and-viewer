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

[Codex reference](https://developers.openai.com/codex/config-reference) · [Claude reference](https://code.claude.com/docs/en/monitoring-usage) · [Copilot reference](https://docs.github.com/en/copilot/reference/enterprise-administrators/enterprise-managed-settings#telemetry)
