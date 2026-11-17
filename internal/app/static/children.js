"use strict";

// The relationship header owns its page and request. A timeline change aborts
// pending expansion, so an older session can never append links to a new one.
const childNavigation = (() => {
  let request = null;
  let current = null;
  function render(page) {
    request?.abort();
    current = { session: page.id, next: page.children_next || "" };
    const root = $("#relations");
    root.innerHTML = (page.parent ? `<a href="${esc(link(page.parent))}">Parent session</a>` : "") +
      subagentLinks(page.children.map((session) => ({ session, label: session.split("/agent:").pop() })));
    bindSubagentLinks(root);
    if (current.next) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "children-more";
      button.textContent = "Load more subagents";
      button.onclick = () => load(button);
      root.append(button);
    }
  }
  async function load(button) {
    request?.abort();
    const controller = new AbortController();
    request = controller;
    const state = current;
    button.disabled = true;
    try {
      const page = await api("/api/session/children?" + new URLSearchParams({ session: state.session, after: state.next }), controller.signal);
      if (controller.signal.aborted || current !== state) return;
      const template = document.createElement("template");
      template.innerHTML = subagentLinks(page.children.map((session) => ({ session, label: session.split("/agent:").pop() })));
      bindSubagentLinks(template.content);
      button.before(template.content);
      state.next = page.next;
      button.hidden = !state.next;
      button.textContent = "Load more subagents";
    } catch (error) {
      if (error.name !== "AbortError" && current === state) button.textContent = "Retry loading subagents";
    } finally {
      if (current === state) button.disabled = false;
    }
  }
  return { render };
})();
