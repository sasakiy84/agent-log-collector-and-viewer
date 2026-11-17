# Reported usage

Session rows show reported input/output tokens, USD cost and error events. Session details add cache reads/writes, cached-input percentage, reasoning tokens and average request time. The shortcut beside timeline pagination stays visible at the latest entries. Explicitly linked model calls also show a short [statistics summary beneath the answer](activity.md).

Totals cover the entire selected session/origin. Event-type filters and timeline pages do not change them. Folder/search/agent filters select whole sessions; folder totals combine each selected session once. Parent and child sessions retain their own usage.

- Cached input is the percentage of input tokens served from cache, not the percentage of requests with a cache hit.
- Input includes reported cache reads/writes. Native Anthropic input counts are normalized to this definition. Reasoning is a subset of output and is shown separately.
- USD uses reported `cost_usd` events and `claude_code.cost.usage` counters with a USD unit. Copilot billing multipliers remain separate event attributes.
- Request time uses reported durations, model span endpoints or `gen_ai.client.operation.duration` histogram sums/counts.
- Error events count flagged logical timeline events; paired imported/OTEL copies count once.
- Missing values display **—**, recorded zeros display **0**. Coverage shows how many sessions report each field. **Partial** indicates incomplete coverage, excluded values or older Codex reports.

Each metric family uses one report source. Imported Codex session counters take precedence for tokens; otherwise OTEL takes precedence over imports. Within an origin, cumulative metrics, delta metrics, request traces and usage events are preferred in that order. The selected basis and alternate origin reports appear under **Usage details**. This avoids adding overlapping representations of the same requests.

Codex imports retain `total_token_usage` alongside individual usage reports. Repeated snapshots use the latest session counter. The offline migration retains prepared accounting and watch checkpoints; unchanged files are not replayed on restart. Codex's context-window placeholders with inconsistent totals are excluded. Claude response IDs collapse repeated reports, and newer ordered fields replace earlier values. Unordered conflicting values are excluded.

Supported OTEL token counters/histograms are `gen_ai.client.token.usage` and `claude_code.token.usage`. Cumulative samples use the latest value per resource/scope/dimensions/start epoch; separate epochs are added. Delta samples require valid start/end times. Exact interval retries collapse; overlapping intervals and contradictory reports are excluded. Unsupported gauge/summary aggregations, missing temporal boundaries, negative/nonfinite values and fractional token counts are excluded. Amounts use exact decimal arithmetic; precision beyond 18 decimal places is excluded. Received originals remain available unchanged.

Measurement points and aggregates update in the ingestion transaction. Reads use cached session totals. An exact integer RTree indexes delta intervals, including out-of-order history. The offline migration preserves and verifies prepared accounting without historical rebuilds on service startup. Archive fingerprints preserve suppression across usage normalization upgrades, while actual counter/response edits remain importable.

```sh
python3 scripts/usage_performance_test.py
AGENTLOG_BIN=./agentlog python3 scripts/usage_browser_test.py
```

All fixtures and screenshots in these checks use isolated synthetic data.
