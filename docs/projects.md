# Projects and tags

| Attribute | Meaning |
| --- | --- |
| `service.namespace` | Standard OTEL resource attribute; Agentlog uses it as the project name |
| `project.tags` | Custom resource attribute; string or string array |

Resource attributes appear in the session header and inspector. Project names and tags are included in session search.

## Claude Code

`~/.claude/settings.json` → `env`:

```json
{
  "OTEL_RESOURCE_ATTRIBUTES": "service.namespace=shop,project.tags=backend"
}
```

## Copilot CLI

`managed-settings.json` → `telemetry`:

```json
{
  "resourceAttributes": {
    "service.namespace": "shop",
    "project.tags": "backend"
  }
}
```

## Codex / endpoint query

Agentlog also accepts `project` and repeated `tag` parameters on each full signal endpoint:

```text
https://agents.example.com/v1/logs?project=shop&tag=backend&tag=go
https://agents.example.com/v1/traces?project=shop&tag=backend&tag=go
https://agents.example.com/v1/metrics?project=shop&tag=backend&tag=go
```

For Codex, set the first two URLs in the `exporter` and `trace_exporter` endpoint values in `~/.codex/config.toml`. Explicit resource labels take precedence over query defaults. These parameters are an Agentlog extension, not an OTLP standard. Use full per-signal URLs; generic base-endpoint query handling varies by CLI.

OTEL resource attributes apply to the process/resource, not automatically to a repository. Fixed labels in global or machine-wide settings apply to all sessions using them. For live per-project labels, use the CLI's supported launch environment or separate configurations; managed policy may override local values. For historical imports, use `--cwd`, `--project` and `--tag` as described in [Import](import.md).

[OTEL service namespace](https://opentelemetry.io/docs/specs/semconv/resource/#service) · [Claude resource attributes](https://code.claude.com/docs/en/monitoring-usage#multi-team-organization-support) · [Copilot resource attributes](https://docs.github.com/en/copilot/reference/enterprise-administrators/enterprise-managed-settings#telemetry)
