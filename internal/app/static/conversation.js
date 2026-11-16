"use strict";
const conversationMatch = new URL(location.href).searchParams.get("match") || "";
const processingStates = new Map();

function resetProcessingGroups() {
  for (const state of processingStates.values()) state.request?.abort();
  processingStates.clear();
}

function conversationTimelineHTML(visible) {
  const groups = meta.processing || [];
  const byBefore = new Map(groups.map((group, index) => [group.before, index]));
  const disclosure = (index) => {
    const group = groups[index];
    processingStates.set(String(index), {
      group, events: [], next: "", loaded: false, request: null,
      diagnostics: group.activity === 0, matches: false, errors: false,
    });
    return `<details class="processing-group" data-processing="${index}"><summary><span>${group.activity ? "Processing details" : "Diagnostics"}<span class="muted"> · ${group.count.toLocaleString()} logs</span></span>${group.errors ? `<button type="button" class="processing-error status failed">${group.errors.toLocaleString()} ${group.errors === 1 ? "error" : "errors"}</button>` : ""}${group.matches ? `<button type="button" class="processing-match">${group.matches.toLocaleString()} ${group.matches === 1 ? "log matches" : "logs match"}</button>` : ""}</summary><div class="processing-body"><div class="processing-options">${group.count > group.activity ? `<label><input type="checkbox" data-processing-diagnostics ${group.activity === 0 ? "checked" : ""}>Include diagnostics</label>` : ""}${group.matches ? '<label><input type="checkbox" data-processing-matches>Matching logs only</label>' : ""}${group.errors ? '<label><input type="checkbox" data-processing-errors>Errors only</label>' : ""}</div><p class="processing-status muted" role="status"></p><div class="processing-events"></div><button type="button" class="processing-more" hidden>Load more logs</button><button type="button" class="processing-retry" hidden>Retry</button></div></details>`;
  };
  let html = visible.length ? "" : '<p class="empty">Conversation text was not recorded.</p>';
  for (const [event, detail] of visible) {
    if (byBefore.has(event.id)) html += disclosure(byBefore.get(event.id));
    const title = ["model", "agent"].includes(detail.kind) ? "Conversation" : detail.title;
    html += eventCardHTML(event, { ...detail, title }, true);
  }
  if (byBefore.has("")) html += disclosure(byBefore.get(""));
  if (visible.at(-1)?.[1].kind === "user" && meta.offset + visible.length >= meta.count)
    html += '<p class="conversation-status muted">No assistant response recorded.</p>';
  return html;
}

function bindProcessingGroups() {
  $("#timeline").querySelectorAll("[data-processing]").forEach((element) => {
    const state = processingStates.get(element.dataset.processing);
    element.addEventListener("toggle", () => {
      if (element.open && !state.loaded && !state.request) loadProcessing(element, state);
      if (!element.open) {
        state.request?.abort();
        state.request = null;
        element.querySelector(".processing-more").disabled = false;
        element.querySelector(".processing-events").setAttribute("aria-busy", "false");
        if (state.loaded) element.querySelector(".processing-status").textContent = "";
        if (inspectedID && element.querySelector(`[data-event="${CSS.escape(inspectedID)}"]`)) closeInspector(false);
      }
    });
    for (const [selector, key] of [["[data-processing-diagnostics]", "diagnostics"], ["[data-processing-matches]", "matches"], ["[data-processing-errors]", "errors"]]) {
      const input = element.querySelector(selector);
      if (input) input.onchange = () => {
        state[key] = input.checked;
        loadProcessing(element, state);
      };
    }
    const match = element.querySelector(".processing-match");
    if (match) match.onclick = (event) => {
      event.preventDefault();
      event.stopPropagation();
      state.matches = state.diagnostics = true;
      state.errors = false;
      for (const input of element.querySelectorAll("[data-processing-diagnostics],[data-processing-matches]")) input.checked = true;
      const errors = element.querySelector("[data-processing-errors]");
      if (errors) errors.checked = false;
      element.open = true;
      loadProcessing(element, state);
    };
    const error = element.querySelector(".processing-error");
    if (error) error.onclick = (event) => {
      event.preventDefault();
      event.stopPropagation();
      state.errors = true;
      state.matches = false;
      element.querySelector("[data-processing-errors]").checked = true;
      const matches = element.querySelector("[data-processing-matches]");
      if (matches) matches.checked = false;
      element.open = true;
      loadProcessing(element, state);
    };
    element.querySelector(".processing-more").onclick = () => loadProcessing(element, state, true);
    element.querySelector(".processing-retry").onclick = () => loadProcessing(element, state, !!state.events.length);
  });
}

async function loadProcessing(element, state, more = false) {
  state.request?.abort();
  const controller = new AbortController();
  state.request = controller;
  if (!more) {
    if (inspectedID && element.querySelector(`[data-event="${CSS.escape(inspectedID)}"]`)) closeInspector(false);
    state.events = [];
    state.loaded = false;
    state.next = "";
    element.querySelector(".processing-events").replaceChildren();
  }
  const status = element.querySelector(".processing-status");
  const moreButton = element.querySelector(".processing-more");
  const retry = element.querySelector(".processing-retry");
  status.textContent = "Loading logs…";
  retry.hidden = true;
  moreButton.disabled = true;
  element.querySelector(".processing-events").setAttribute("aria-busy", "true");
  const params = new URLSearchParams({
    session: selected, origin: pageOrigin, after: state.group.after,
    before: state.group.before, diagnostics: state.diagnostics ? "1" : "0",
  });
  if (more && state.next) params.set("cursor", state.next);
  if (state.errors) params.set("errors", "1");
  if (state.matches && conversationMatch) {
    params.set("matches", "1");
    params.set("match", conversationMatch);
  }
  try {
    const page = await api("/api/processing?" + params, controller.signal);
    if (controller.signal.aborted || !element.isConnected) return;
    state.events.push(...page.events);
    state.next = page.next;
    state.loaded = true;
    const container = element.querySelector(".processing-events");
    container.insertAdjacentHTML("beforeend", page.events.map((event) => eventCardHTML(event)).join(""));
    bindEventCards(container);
    bindModelCalls(container);
    bindSubagentLinks(container);
    status.textContent = state.events.length ? "" : "No logs match these filters.";
    moreButton.hidden = !state.next;
  } catch (error) {
    if (error.name === "AbortError" || !element.isConnected) return;
    status.textContent = error.message;
    if (error.message.includes("HTTP 409")) {
      status.textContent = "Logs changed. Reload the session to update this range.";
      retry.textContent = "Reload session";
      retry.onclick = () => refresh("preserve");
    } else {
      retry.textContent = "Retry";
      retry.onclick = () => loadProcessing(element, state, !!state.events.length);
    }
    retry.hidden = false;
  } finally {
    if (state.request === controller) {
      state.request = null;
      moreButton.disabled = false;
      element.querySelector(".processing-events").setAttribute("aria-busy", "false");
    }
  }
}
