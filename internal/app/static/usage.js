"use strict";

const usageIntegerFormat = new Intl.NumberFormat("en-US");
const usageCompactFormat = new Intl.NumberFormat("en-US", {
  notation: "compact",
  maximumFractionDigits: 1,
});
const usageNumber = (value) =>
  value === undefined ? "—" : usageIntegerFormat.format(BigInt(value));
const usageCompact = (value) =>
  value === undefined ? "—" : usageCompactFormat.format(BigInt(value));
const usageUSD = (value) => {
  if (value === undefined) return "—";
  const [whole, fraction] = value.split(".");
  return "$" + usageNumber(whole) + (fraction ? "." + fraction : "");
};
const usageTokenKeys = [
  "input_tokens",
  "output_tokens",
  "cache_read_tokens",
  "cache_write_tokens",
  "reasoning_tokens",
];
const usagePartial = (s) =>
  ["input_tokens", "output_tokens", "cost_usd"].some(
    (k) => s.values[k] !== undefined && (s.coverage[k] || 0) < s.sessions,
  ) ||
  (usageTokenKeys.some((k) => s.values[k] !== undefined) &&
    (s.values.input_tokens === undefined ||
      s.values.output_tokens === undefined)) ||
  s.excluded > 0 ||
  s.legacy > 0;
function usageTitle(s) {
  const labels = {
    input_tokens: "Input tokens",
    output_tokens: "Output tokens",
    cache_read_tokens: "Cache read",
    cache_write_tokens: "Cache write",
    reasoning_tokens: "Reasoning",
    cost_usd: "USD cost",
  };
  return (
    (Object.entries(labels)
      .filter(([key]) => s.values[key] !== undefined)
      .map(
        ([key, label]) =>
          `${label}: ${key === "cost_usd" ? usageUSD(s.values[key]) : usageNumber(s.values[key])} · ${s.coverage[key] || 0}/${s.sessions} sessions reporting`,
      )
      .join("\n") || "Usage not reported") +
    (s.overlap ? "\nOne report source is used per metric." : "")
  );
}
function usageRow(s) {
  const v = s.values,
    known = v.input_tokens !== undefined || v.output_tokens !== undefined;
  const errors = BigInt(v.errors || "0");
  const partialTokens =
    v.cache_read_tokens !== undefined
      ? usageCompact(v.cache_read_tokens) + " cached"
      : v.reasoning_tokens !== undefined
        ? usageCompact(v.reasoning_tokens) + " reasoning"
        : v.cache_write_tokens !== undefined
          ? usageCompact(v.cache_write_tokens) + " cache write"
          : "";
  const extra = [
    v.cost_usd !== undefined ? `<span>${esc(usageUSD(v.cost_usd))}</span>` : "",
    errors > 0
      ? `<span class="usage-error">${esc(usageNumber(v.errors))} ${errors === 1n ? "error" : "errors"}</span>`
      : "",
    usagePartial(s) ? '<span class="usage-partial">Partial</span>' : "",
  ]
    .filter(Boolean)
    .join(" · ");
  return `<span class="usage-row" title="${esc(usageTitle(s))}">${known ? `<span>${esc(usageCompact(v.input_tokens))} <span class="muted">in</span> · ${esc(usageCompact(v.output_tokens))} <span class="muted">out</span></span>` : partialTokens ? `<span>${esc(partialTokens)}</span>` : v.cost_usd !== undefined || errors > 0 ? "" : '<span class="muted">—</span>'}${extra ? `<small>${extra}</small>` : ""}</span>`;
}
function usageBasis(value) {
  const origin = value?.startsWith("import/")
    ? "Imported"
    : value?.startsWith("otel/")
      ? "OTEL"
      : "";
  const kind = value?.split("/").pop();
  return (
    [
      origin,
      {
        "codex-total": "session counters",
        "metric-cumulative": "cumulative metrics",
        "metric-delta": "delta metrics",
        traces: "request traces",
        logs: "usage events",
      }[kind],
    ]
      .filter(Boolean)
      .join(" ") || "—"
  );
}
function usageShortcut(s) {
  const v = s.values;
  let tokens = "";
  if (v.input_tokens !== undefined && v.output_tokens !== undefined)
    tokens =
      usageCompact(String(BigInt(v.input_tokens) + BigInt(v.output_tokens))) +
      " tokens";
  else if (v.output_tokens !== undefined)
    tokens = usageCompact(v.output_tokens) + " out";
  else if (v.input_tokens !== undefined)
    tokens = usageCompact(v.input_tokens) + " in";
  const text = [tokens, v.cost_usd !== undefined ? usageUSD(v.cost_usd) : ""]
    .filter(Boolean)
    .join(" · ");
  return `<a class="usage-shortcut" href="#sessionusage" aria-label="View session usage" title="${esc(usageTitle(s))}">${esc(text || "Usage")}</a>`;
}
function usageDetails(s) {
  const v = s.values,
    known =
      usageTokenKeys.some((k) => v[k] !== undefined) ||
      v.cost_usd !== undefined;
  const item = (label, value, title = "") =>
    `<div${title ? ` title="${esc(title)}"` : ""}><dt>${esc(label)}</dt><dd>${esc(value)}</dd></div>`;
  let hit = "—";
  if (
    v.input_tokens !== undefined &&
    v.cache_read_tokens !== undefined &&
    BigInt(v.input_tokens) > 0n &&
    BigInt(v.cache_read_tokens) <= BigInt(v.input_tokens)
  ) {
    const n =
      (BigInt(v.cache_read_tokens) * 1000n + BigInt(v.input_tokens) / 2n) /
      BigInt(v.input_tokens);
    hit = `${n / 10n}.${n % 10n}%`;
  }
  let latency = "—";
  if (Number(v.duration_samples) > 0) {
    const ms = Number(v.duration_ms) / Number(v.duration_samples);
    latency =
      ms >= 1000 ? (ms / 1000).toFixed(2) + " s" : Math.round(ms) + " ms";
  }
  const reporting = ["input_tokens", "output_tokens", "cost_usd"]
    .filter((k) => v[k] !== undefined)
    .map(
      (k) =>
        `${{ input_tokens: "Input", output_tokens: "Output", cost_usd: "Cost" }[k]}: ${s.coverage[k] || 0}/${s.sessions} sessions`,
    )
    .join(" · ");
  const variants = s.variants
    ? `<p>Reports may cover the same requests. Each metric uses one report source.</p><table class="usage-sources"><thead><tr><th>Source</th><th>Input</th><th>Output</th><th>USD</th></tr></thead><tbody>${Object.entries(
        s.variants,
      )
        .map(
          ([source, u]) =>
            `<tr><th>${source === "import" ? "Imported" : "OTEL"}</th><td>${esc(usageNumber(u.values.input_tokens))}</td><td>${esc(usageNumber(u.values.output_tokens))}</td><td>${esc(usageUSD(u.values.cost_usd))}</td></tr>`,
        )
        .join("")}</tbody></table>`
    : "";
  return `<div class="usage-heading">Reported usage <span class="muted">· entire session</span>${usagePartial(s) ? '<span class="usage-partial">Partial</span>' : ""}</div>${known ? `<dl class="usage-totals">${item("Input", usageNumber(v.input_tokens), "Includes reported cache tokens")}${item("Output", usageNumber(v.output_tokens))}${item("Cache read", usageNumber(v.cache_read_tokens))}${item("Cached input", hit, "Cache-read tokens as a percentage of input tokens")}${item("USD cost", usageUSD(v.cost_usd), "Reported USD only; billing multipliers are separate")}${item("Error events", usageNumber(v.errors || "0"))}${v.output_tokens === undefined && v.reasoning_tokens !== undefined ? item("Reasoning", usageNumber(v.reasoning_tokens)) : ""}</dl>` : `<p class="usage-unreported">Usage not reported${BigInt(v.errors || "0") > 0n ? " · " + usageNumber(v.errors) + " errors" : ""}</p>`}<details class="usage-more"><summary>Usage details</summary><dl class="fields">${item("Cache write", usageNumber(v.cache_write_tokens))}${item("Reasoning", usageNumber(v.reasoning_tokens), "Included in output tokens")}${item("Avg request time", latency, v.duration_samples ? `${v.duration_samples} reported durations` : "")}${item("Tokens from", usageBasis(s.basis?.tokens))}${item("Cost from", usageBasis(s.basis?.cost))}</dl>${reporting ? `<p>${esc(reporting)}</p>` : ""}${variants}${s.overlap && !s.variants ? "<p>Each session uses one report source per metric.</p>" : ""}${s.excluded ? `<p>${s.excluded} reports excluded: invalid values, unsupported aggregation or overlapping intervals.</p>` : ""}${s.legacy ? "<p>Older Codex usage reports need reimporting for session totals. Watched files refresh automatically on restart.</p>" : ""}</details>`;
}
