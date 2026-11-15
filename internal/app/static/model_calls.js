"use strict";
function modelCallsHTML(event) {
  if (!event.model_calls?.length) return "";
  return `<div class="model-calls" aria-label="Model call statistics">${event.model_calls
    .map((call) => {
      const v = call.values,
        parts = [call.model];
      if (v.input_tokens !== undefined)
        parts.push(usageCompact(v.input_tokens) + " in");
      if (v.output_tokens !== undefined)
        parts.push(usageCompact(v.output_tokens) + " out");
      if (v.cache_read_tokens !== undefined)
        parts.push(usageCompact(v.cache_read_tokens) + " cached");
      if (v.cost_usd !== undefined) parts.push(usageUSD(v.cost_usd));
      if (call.duration_ms)
        parts.push(
          Number(call.duration_ms) >= 1000
            ? (Number(call.duration_ms) / 1000).toFixed(2) + " s"
            : Math.round(Number(call.duration_ms)) + " ms",
        );
      const full = [
        call.model,
        ...Object.entries(v).map(([key, value]) => key + ": " + value),
        call.duration_ms ? call.duration_ms + " ms" : "",
      ]
        .filter(Boolean)
        .join("\n");
      return `<button data-model-call="${esc(call.id)}" title="${esc(full)}">${esc(parts.filter(Boolean).join(" · ") || "Model call statistics")}</button>`;
    })
    .join(
      "",
    )}${event.model_calls_more ? '<span class="muted">More calls in Diagnostics</span>' : ""}</div>`;
}
function bindModelCalls(root) {
  root.querySelectorAll("[data-model-call]").forEach(
    (button) =>
      (button.onclick = (event) => {
        event.stopPropagation();
        openInspector(button.dataset.modelCall);
      }),
  );
}
