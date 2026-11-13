# Sender configuration

Examples below target `https://agents.example.com`. Replace the placeholder token with the server token. Per-signal OTLP endpoints include `/v1/logs`, `/v1/traces` or `/v1/metrics`; generic OTLP environment endpoints use the base URL. Check your installed CLI version: available signals and fields vary. These examples were checked against the linked official references; end-to-end testing here uses synthetic OTLP rather than authenticated real CLI runs.

## Codex CLI

In `~/.codex/config.toml` (merge with your existing `[otel]` table):

```toml
[otel]
log_user_prompt = false
exporter = { otlp-http = { endpoint = "https://agents.example.com/v1/logs", protocol = "binary", headers = { "Authorization" = "Bearer REPLACE_WITH_TOKEN" } } }
trace_exporter = { otlp-http = { endpoint = "https://agents.example.com/v1/traces", protocol = "binary", headers = { "Authorization" = "Bearer REPLACE_WITH_TOKEN" } } }
```

Codex uses `binary`/`json` for the HTTP protocol configuration, rather than the standard environment variable spellings. Keep configuration permissions private because these headers contain your token. Metrics export configuration depends on Codex version; start with logs and traces above. See [Codex configuration reference](https://developers.openai.com/codex/config-reference).

## Claude Code CLI

Merge this `env` object into **`~/.claude/settings.json`**, preserving your existing settings and environment entries. On Windows the default path is `%USERPROFILE%\.claude\settings.json`. If you use `CLAUDE_CONFIG_DIR`, use the settings file in that directory instead.

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_METRICS_EXPORTER": "otlp",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "https://agents.example.com",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer REPLACE_WITH_TOKEN"
  }
}
```

Replace the endpoint and token, save, and start a new `claude` session. No shell `export` is needed. Claude settings use strict JSON; do not add comments. Keep the token-bearing file private to your user. Repository-local `.claude/settings.json` and `.claude/settings.local.json` cannot enable or redirect OTLP export, so use the user-level path above. Organization-managed settings can override it.

For beta traces on supported versions, add `"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1"` and `"OTEL_TRACES_EXPORTER": "otlp"` inside the same `env` object. To retain conversation/tool content, optionally add `"OTEL_LOG_USER_PROMPTS": "1"`, `"OTEL_LOG_ASSISTANT_RESPONSES": "1"` and `"OTEL_LOG_TOOL_DETAILS": "1"` there; otherwise content remains subject to the CLI's redaction defaults. Metrics without session attributes appear under `unassigned:claude`.

References: [Claude monitoring](https://code.claude.com/docs/en/monitoring-usage#administrator-configuration) · [Settings locations](https://code.claude.com/docs/en/settings#settings-files-and-who-they-affect).

## GitHub Copilot CLI

To configure the endpoint, protocol and authentication entirely in a file, use Copilot CLI's **file-based managed settings**. This is a machine-wide policy, requires administrator access to install, and takes precedence over local settings and environment variables. Merge the following `telemetry` object into the existing managed file; preserve unrelated organization policy.

| OS | Managed settings file |
| --- | --- |
| Linux | `/etc/github-copilot/managed-settings.json` |
| macOS | `/Library/Application Support/GitHubCopilot/managed-settings.json` |
| Windows | `%ProgramFiles%\GitHubCopilot\managed-settings.json` |

```json
{
  "telemetry": {
    "enabled": true,
    "endpoint": "https://agents.example.com",
    "protocol": "http/protobuf",
    "headers": {
      "Authorization": "Bearer REPLACE_WITH_TOKEN"
    }
  }
}
```

Replace the endpoint and token, save, and start a new `copilot` session. No shell `export` is needed. On macOS/Linux the managed file must be owned by root, must not be a symlink, and must not be world-writable. It must also be readable by the CLI user; restrict read access to the intended users/group because it contains the collector token. On Windows restrict file access to the intended users and administrators.

Use HTTPS: Copilot disables OTLP export for HTTP endpoints. Its documented signals are traces and metrics; content capture defaults off. For conversation and tool content, optionally add `"capture": { "prompts": true, "responses": true, "toolArguments": true, "toolOutput": true }` inside `telemetry`. Enable only the content you want retained by Agentlog.

The current CLI uses `~/.copilot/settings.json` for personal settings (`COPILOT_HOME` changes that directory); `config.json` holds internal state and ignores user settings. The official personal-settings documentation describes `telemetry.headersProvider` for dynamic authentication, while the complete endpoint/protocol/static-header policy above is documented under managed settings. Do not assume the machine-wide example can be moved into a personal or repository settings file unchanged. If you cannot install managed settings, the supported shell alternative is:

```sh
export COPILOT_OTEL_ENABLED=true
export OTEL_EXPORTER_OTLP_ENDPOINT=https://agents.example.com
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer REPLACE_WITH_TOKEN"
```

These settings refer to the CLI runtime, whose default service name is `github-copilot`, rather than the separate VS Code extension telemetry. Check your installed CLI's support before applying settings.

References: [Managed settings file locations](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference#mdm-managed-settings-sources) · [Telemetry policy](https://docs.github.com/en/copilot/reference/enterprise-administrators/enterprise-managed-settings#telemetry) · [OpenTelemetry reference](https://docs.github.com/en/copilot/reference/enterprise-administrators/opentelemetry-reference).
