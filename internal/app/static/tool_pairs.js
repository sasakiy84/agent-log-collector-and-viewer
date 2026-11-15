"use strict";
function toolPhaseLabel(detail) {
  if (detail.kind !== "tool") return detail.kind;
  if (
    detail.blocks.some((b) => ["Input", "Command", "File"].includes(b.label)) &&
    detail.blocks.some((b) => b.label === "Output")
  )
    return "Input / output";
  let phase = detail.tool_phase;
  if (!phase) {
    const name = detail.name.replace(
      /^(codex|claude_code|github\.copilot|copilot)\./,
      "",
    );
    if (
      [
        "gen_ai.tool.start",
        "tool_call",
        "tool_use",
        "tool.execution_start",
      ].includes(name)
    )
      phase = "tool.start";
    else if (
      ["gen_ai.tool.result", "tool_result", "tool.execution_complete"].includes(
        name,
      )
    )
      phase = "tool.result";
  }
  return phase === "tool.start"
    ? "Input"
    : phase === "tool.result"
      ? "Output"
      : "Tool";
}
function toolPairHTML(event) {
  const pair = event.tool_pair;
  if (!pair || ["embedded", "unavailable"].includes(pair.state)) return "";
  const direction = pair.phase === "tool.start" ? "output" : "input";
  if (pair.state !== "paired")
    return `<p class="toolpair-status">${esc(pair.state === "ambiguous" ? "Multiple or conflicting call records; no automatic link." : pair.state === "no_id" ? "Tool call ID not exported." : `${direction === "output" ? "Output" : "Input"} not collected.`)}</p>`;
  const other = pair.counterpart,
    p = other.detail;
  const blocks = p.blocks.filter((b) =>
    direction === "output"
      ? ["Output", "Error"].includes(b.label)
      : !["Output", "Error"].includes(b.label),
  );
  return `<section class="toolpair ${p.error ? "error" : ""}" aria-label="Related tool ${direction}"><div class="toolpair-heading"><h3>${direction === "output" ? "Output" : "Input"}</h3>${p.error ? '<span class="status failed">Failed</span>' : ""}<button type="button" data-tool-event="${esc(other.id)}">Open ${direction} event</button><time>${esc(date(other.time))}</time></div><div class="toolpair-preview">${blocks.map((b) => `<div class="block"><div class="blocklabel">${esc(b.label)}</div><${b.code ? "pre" : "div"} class="${b.code ? "code" : "prose"}">${esc(b.text)}</${b.code ? "pre" : "div"}></div>`).join("") || '<p class="muted">Content not exported</p>'}</div>${p.fields.length ? `<dl class="fields">${p.fields.map(([key, value]) => `<div><dt>${esc(key)}</dt><dd>${esc(value)}</dd></div>`).join("")}</dl>` : ""}</section>`;
}
function bindToolPairs(root) {
  root.querySelectorAll("[data-tool-event]").forEach(
    (button) =>
      (button.onclick = () => {
        const trigger = inspectorTrigger;
        openInspector(button.dataset.toolEvent);
        if (trigger?.isConnected) inspectorTrigger = trigger;
      }),
  );
}
