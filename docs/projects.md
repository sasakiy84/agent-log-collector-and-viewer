# Projects and tags

| Attribute | Meaning |
| --- | --- |
| `service.namespace` | Standard OTEL resource attribute; Agentlog uses it as the project name |
| `project.tags` | Custom resource attribute; string or string array |

The session search page keeps projects in a persistent sidebar, ordered by their most recent session activity. Click a project to filter sessions, or combine several with their checkboxes; text search and [time/agent/origin filters](search-filters.md) apply within that selection. Projects have their own search. Selection is saved in the URL and restored when returning from a session.

Sidebar counts and ordering follow the visible root sessions: subagent sessions
are excluded, and internal conversations are excluded unless **Internal
sessions** is enabled. Projects containing only hidden sessions are omitted.
**Export entire project** in the list’s Export menu uses an inclusive scope, so complete project exports still include
all children and internal sessions of the selected projects.

Projects are grouped by name. Agentlog uses an explicit `service.namespace` / `project.name` label when present; otherwise it uses the final directory name from the exported working directory. For example, the label `shop`, `/workspaces/shop` and `/home/user/shop` all belong to the same project. Windows and POSIX directory separators are supported. Names match exactly, including case. Sessions with neither a label nor a working directory appear under **Unassigned**. Paths mentioned in prompts or tool arguments are never used to guess a project.

The same name groups session search, project counts, skills and exports. The original working directory remains available in session details and folder-based archives. Existing cached project groups are merged once by a versioned migration without re-reading raw event histories. Older `project=path:…` selections resolve to the new name group, so open pages and saved links do not recreate duplicate entries.

Resource attributes appear in the session header and inspector. Project names and tags are included in session search.

## Codex

```sh
OTEL_RESOURCE_ATTRIBUTES='service.namespace=shop,project.tags=backend' codex
```

Current Codex uses these Resource attributes for logs and traces. `config.toml` has no arbitrary Resource-attribute table; `otel.span_attributes` adds attributes only to trace spans.

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

## Endpoint query

Agentlog also accepts `project` and repeated `tag` parameters on each full signal endpoint:

```text
https://agents.example.com/v1/logs?project=shop&tag=backend&tag=go
https://agents.example.com/v1/traces?project=shop&tag=backend&tag=go
https://agents.example.com/v1/metrics?project=shop&tag=backend&tag=go
```

For Codex, set the first two URLs in the `exporter` and `trace_exporter` endpoint values in `~/.codex/config.toml`. For Claude Code, use `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` in `env`. [Complete CLI settings and project URL examples](senders.md). Copilot uses `telemetry.resourceAttributes` as shown above; keep its `telemetry.endpoint` as the base URL.

Explicit `service.namespace` or `project.name` resource attributes take precedence over query defaults. These parameters are an Agentlog extension, not an OTLP standard. Use full per-signal URLs; generic base-endpoint query handling varies by CLI.

OTEL resource attributes apply to the process/resource, not automatically to a repository. Fixed labels in global or machine-wide settings apply to all sessions using them. For live per-project labels, use the CLI's supported launch environment or separate configurations; managed policy may override local values. For historical imports, use `--cwd`, `--project` and `--tag` as described in [Import](import.md).

[Codex implementation](https://github.com/openai/codex/blob/main/codex-rs/otel/src/provider.rs#L411) · [OTEL service namespace](https://opentelemetry.io/docs/specs/semconv/resource/#service) · [Claude resource attributes](https://code.claude.com/docs/en/monitoring-usage#multi-team-organization-support) · [Copilot resource attributes](https://docs.github.com/en/copilot/reference/enterprise-administrators/enterprise-managed-settings#telemetry)
