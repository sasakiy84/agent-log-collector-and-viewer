"use strict";
const $ = (s) => document.querySelector(s);
// Wrapped filters determine the sticky inset; keep drawer and scroll targets
// below the toolbar at every viewport width and browser zoom level.
const timelineToolbarObserver = new ResizeObserver(([entry]) => {
  if (entry.contentRect.height > 0)
    $("#detailpage").style.setProperty(
      "--timeline-toolbar-height",
      entry.target.getBoundingClientRect().height + "px",
    );
});
timelineToolbarObserver.observe($(".logbar"));

const esc = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const label = (s) =>
  ({
    codex: "Codex",
    claude: "Claude Code",
    copilot: "Copilot",
    other: "Other",
  })[s] || s;
const link = (id) => "/sessions/" + encodeURIComponent(id);
const dateFormat = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});
const clockFormat = new Intl.DateTimeFormat(undefined, {
  hour: "numeric",
  minute: "numeric",
  second: "numeric",
  hour12: false,
});
const date = (s) => dateFormat.format(new Date(s));
const clock = (s) => clockFormat.format(new Date(s));
// Labels belong to the view: cached cards and full inspector records use the
// same wording without rewriting stored telemetry or rebuilding projections.
function eventTitle(detail) {
  if (detail.kind !== "model" || detail.title === "Token usage")
    return detail.title;
  if (detail.name.includes("sse_event")) return "Model stream event";
  return detail.fields.some(([label]) =>
    [
      "Input tokens",
      "Output tokens",
      "Cache read",
      "Cache write",
      "Reasoning",
      "Cost (USD)",
      "Billing multiplier",
      "Nano AIU",
      "Duration",
    ].includes(label),
  )
    ? "Model call statistics"
    : "Model API event";
}
function blockLabel(block, detail) {
  switch (block.label) {
    case "Request":
      return "User message";
    case "Response":
      return detail.kind === "assistant" ? "Assistant message" : "Model output";
    case "Input":
      return detail.kind === "model" ? "Model input" : block.label;
    case "Output":
      return detail.kind === "model" ? "Model output" : block.label;
    default:
      return block.label;
  }
}
let requestSerial = 0,
  activeRequest = null,
  sessionOffset = 0,
  eventOffset = null,
  eventLimit = 200,
  query = "",
  kind = "",
  view = "activity",
  origin = "",
  pageOrigin = "",
  events = [],
  meta = null;
let sessionData = null;
let inspectorTrigger = null;
let inspectedID = "",
  inspectedRecord = null,
  inspectorTab = "details",
  inspectorEvent = null,
  inspectorRequest = null,
  jsonEvent = null;
const inspectorCache = new Map();
let selected = location.pathname.startsWith("/sessions/")
  ? decodeURIComponent(location.pathname.slice(10))
  : "";
const archiveMode = location.pathname === "/archive";
const skillsMode = location.pathname === "/skills";
const exportMode = location.pathname === "/export";
let exportDownloadURL = "";
let exportQueryPending = false;
const listURL = new URL(location.href);
let searchScope = readSearchScope(listURL.searchParams);
let projectSelections = [...new Set(listURL.searchParams.getAll("project").map(canonicalProjectID))];
if (projectSelections.length <= 1) projectSelections = [];
let skillFilter = listURL.searchParams.get("skill") || "",
  skillPlugin = listURL.searchParams.get("plugin") || "";
let project = listURL.searchParams.has("project")
  ? canonicalProjectID(listURL.searchParams.get("project"))
  : null;
let folder = listURL.searchParams.has("folder")
  ? listURL.searchParams.get("folder")
  : null;
if (projectSelections.length > 1) project = null;
$("#showdiagnostics").checked = listURL.searchParams.has("diagnostics")
  ? listURL.searchParams.get("diagnostics") === "1"
  : exportMode;
$("#exportcontrols").hidden = !exportMode;
$("#exportchildren").checked = listURL.searchParams.get("include_children") !== "0";
query = listURL.searchParams.get("q") || "";
let idKey = listURL.searchParams.get("id_key") || "",
  idValue = listURL.searchParams.get("id_value") || "";
sessionOffset = Number(listURL.searchParams.get("offset")) || 0;
if (selected) {
  view =
    listURL.searchParams.get("view") === "diagnostics"
      ? "diagnostics"
      : "activity";
  kind = [
    "conversation",
    "tool",
    "error",
    "model",
    "metric",
    "skill",
    "agent",
    "event",
  ].includes(listURL.searchParams.get("kind"))
    ? listURL.searchParams.get("kind")
    : "";
  const page = Number(listURL.searchParams.get("page"));
  if (
    Number.isSafeInteger(page) &&
    page > 0 &&
    page <= Number.MAX_SAFE_INTEGER / 200
  )
    eventOffset = (page - 1) * 200;
  origin = ["otel", "import"].includes(listURL.searchParams.get("origin"))
    ? listURL.searchParams.get("origin")
    : "";
  $("#origin").value = origin;
}
$("#search").value = query;
syncIdentifierSearch();
if (!selected && !archiveMode)
  sessionStorage.setItem("sessionsURL", location.pathname + location.search);
$("#detailpage > .back").href = sessionStorage.getItem("sessionsURL") || "/";
function updateDocumentTitle() {
  let parts;
  if (!$("#login").hidden) {
    parts = ["Sign in"];
  } else if (selected) {
    const preview = meta?.preview;
    const context = typeof preview?.project === "string" && preview.project
      ? preview.project
      : typeof preview?.cwd === "string" && preview.cwd ? folderName(preview.cwd) : "";
    parts = [$("#sessiontitle").textContent, context];
  } else if (archiveMode) {
    parts = ["Archive"];
  } else {
    const scope = projectScopeName() || (folder !== null ? folderName(folder) : "");
    parts = [scope, skillsMode ? "Skills" : exportMode ? "Export" : "Sessions"];
  }
  document.title = [...parts, "Agentlog"]
    .map((part) => String(part || "").replace(/\s+/g, " ").trim())
    .filter(Boolean).join(" · ");
}
async function apiResponse(path, signal) {
  const r = await fetch(path, { signal });
  if (r.status === 401) {
    closeInspector(false);
    $("#login").hidden = false;
    $("#workspace").hidden = true;
    updateDocumentTitle();
    throw Error("Sign in required.");
  }
  if (!r.ok)
    throw Error(
      r.status === 404
        ? "Session not found."
        : `Unable to load (HTTP ${r.status}).`,
    );
  return r;
}
async function api(path, signal) {
  return (await apiResponse(path, signal)).json();
}
function titleFor(id, p) {
  if (p) {
    const prompt = p.blocks.find((b) => /request|user/i.test(b.label));
    if (prompt && prompt.text !== "Content not exported")
      return prompt.text.split("\n")[0].slice(0, 150);
  }
  return id.includes("/agent:")
    ? "Subagent · " + id.split("/agent:").pop()
    : id.replace(/^(codex|claude|copilot|other):/, "");
}
function listParameters() {
  const p = new URLSearchParams();
  appendSearchScope(p);
  if (exportMode && !$("#exportchildren").checked) p.set("include_children", "0");
  if (skillFilter) {
    p.set("skill", skillFilter);
    p.set("plugin", skillPlugin);
  }
  if ($("#showdiagnostics").checked) p.set("diagnostics", "1");
  else if (exportMode) p.set("diagnostics", "0");
  if (query) p.set("q", query);
  if (idValue) {
    p.set("id_value", idValue);
    if (idKey) p.set("id_key", idKey);
  }
  return p;
}
function listState() {
  const p = listParameters();
  if (folder !== null) p.set("folder", folder);
  if (sessionOffset) p.set("offset", sessionOffset);
  history.replaceState(null, "", (exportMode ? "/export" : "/") + (p.size ? "?" + p : ""));
  sessionStorage.setItem("sessionsURL", location.pathname + location.search);
}
function folderName(path) {
  return path
    ? path
        .replace(/[\\/]+$/, "")
        .split(/[\\/]/)
        .pop() || path
    : "Folder unavailable";
}
function renderListHeading(total) {
  $("#skillfilter").hidden = !skillFilter;
  $("#skillfilterlabel").textContent = skillFilter
    ? "Skill: " + skillFilter + (skillPlugin ? " · " + skillPlugin : "")
    : "";
  $("#listtitle").textContent =
    (exportMode ? "Export · " : "") + (selectedProjects().length
      ? projectScopeName()
      : folder !== null
        ? folderName(folder)
        : "All sessions");
  $("#foldersback").hidden = !exportMode;
  if (exportMode) {
    $("#foldersback").textContent = "Sessions";
    $("#foldersback").href = project === null ? "/" : "/?" + new URLSearchParams({ project });
  }
  const path = project?.startsWith("path:") ? project.slice(5) : folder;
  $("#folderpath").hidden = !path;
  $("#folderpath").textContent = path || "";
  $("#listcount").textContent = `${total.toLocaleString()} sessions`;
  $("#listcolumn").textContent = "Session";
  $("#countcolumn").textContent = exportMode ? "Records" : "Events";
  sessionPager.noun = "sessions";
  renderProjects();
}
function detailState(latest) {
  const p = new URLSearchParams();
  if (!latest) p.set("page", Math.floor(meta.offset / meta.limit) + 1);
  if (pageOrigin) p.set("origin", pageOrigin);
  if (view !== "activity") p.set("view", view);
  if (kind) p.set("kind", kind);
  history.replaceState(null, "", link(selected) + (p.size ? "?" + p : ""));
}
function projectLabel(p) {
  return [
    p?.project,
    ...(Array.isArray(p?.tags) ? p.tags : p?.tags ? [p.tags] : []),
  ]
    .filter(Boolean)
    .join(" · ");
}
function renderSessions(rows, total) {
  renderListHeading(total);
  $("#sessions").innerHTML = rows.length
    ? rows
        .slice(0, 200)
        .map(
          (s) =>
            `<a class="session" href="${esc(link(s.id))}" data-session="${esc(s.id)}"><div><h2>${esc(titleFor(s.id, s.preview))}</h2><small>${projectLabel(s.preview) ? esc(projectLabel(s.preview)) + " · " : ""}${esc(s.id)}${s.parent ? " · Subagent" : ""}</small></div><span class="source">${esc(label(s.source))}</span><time>${esc(date(s.end))}</time>${usageRow(s.usage)}<span class="count">${s.count}</span></a>`,
        )
        .join("")
    : '<p class="empty">No sessions</p>';
  sessionPager.update({ total, offset: sessionOffset, size: 200 });
}

function renderEvents() {
  const focusedID = document.activeElement?.dataset.event;
  const slice = events.slice(0, meta.limit),
    parsed = slice.map((e) => [e, e.detail]);
  const inspected = events.find(
    (e) => e.id === inspectedID || e.records.some((r) => r.id === inspectedID),
  );
  if (inspected) inspectedID = inspected.id;
  const visible = parsed;
  $("#sessiontitle").textContent = titleFor(selected, meta.preview);
  const usageKey = JSON.stringify(meta.usage);
  if ($("#sessionusage").dataset.key !== usageKey) {
    $("#sessionusage").innerHTML = usageDetails(meta.usage);
    $("#sessionusage").dataset.key = usageKey;
  }
  $("#export").href =
    "/api/export?session=" +
    encodeURIComponent(selected) +
    "&origin=" +
    encodeURIComponent(pageOrigin);
  const context = {};
  for (const key of ["model", "cwd", "host", "project", "tags"])
    context[key] =
      parsed.map(([, p]) => p).find((p) => p[key])?.[key] ??
      meta.preview?.[key];
  $("#sessionmeta").innerHTML =
    `<strong>${esc(label(meta.source))}</strong>${meta.start ? `<span>${esc(date(meta.start))}</span>` : ""}${context.model ? `<span>${esc(context.model)}</span>` : ""}${context.cwd ? `<span>${esc(context.cwd)}</span>` : ""}${context.host ? `<span>${esc(context.host)}</span>` : ""}${context.project ? `<span>Project: ${esc(context.project)}</span>` : ""}${context.tags ? `<span>Tags: ${esc(Array.isArray(context.tags) ? context.tags.join(", ") : context.tags)}</span>` : ""}<span class="sessionid">${esc(selected)}</span>`;
  $("#sessionskills").innerHTML = sessionSkillsHTML(meta.skills);
  bindSkillEvents($("#sessionskills"));
  $("#relations").innerHTML =
    (meta.parent
      ? `<a href="${esc(link(meta.parent))}">Parent session</a>`
      : "") +
    subagentLinks(
      meta.children.map((id) => ({
        session: id,
        label: id.split("/agent:").pop(),
      })),
    );
  $("#pageinfo").textContent = meta.count
    ? `${meta.offset + 1}–${meta.offset + slice.length} / ${meta.count}${meta.total_count !== undefined ? " · " + meta.total_count + " total events" : ""}`
    : "0 events";
  $("#timeline").innerHTML = visible.length
    ? visible
        .map(
          ([e, p]) =>
            `<article class="event ${esc(p.kind)} ${p.error ? "error" : ""}" id="span-${esc(e.span || e.id)}" data-event="${esc(e.id)}" role="button" tabindex="0" aria-label="${esc(eventTitle(p) + " · " + clock(e.time))}" aria-expanded="${e.id === inspectedID}" aria-controls="inspector"><div class="eventcontent"><div class="eventheading"><h2>${esc(eventTitle(p))}</h2>${e.origins.length > 1 ? '<span class="kind">Imported + OTEL</span>' : ""}${["tool", "metric", "skill"].includes(p.kind) ? `<span class="kind">${esc(toolPhaseLabel(p))}</span>` : ""}${p.error ? '<span class="status failed">Failed</span>' : String(p.attributes.success) === "true" ? '<span class="status succeeded">Succeeded</span>' : ""}<time datetime="${esc(e.time)}" title="${esc(date(e.time))}">${esc(clock(e.time))}</time></div>${p.blocks.map((b) => `<div class="block ${b.label === "Error" ? "failure-detail" : ""}">${["Request", "Response", "Message", p.kind].includes(b.label) ? "" : `<div class="blocklabel">${esc(blockLabel(b, p))}</div>`}<${b.code ? "pre" : "div"} class="${b.code ? "code" : "prose"}">${esc(b.text)}</${b.code ? "pre" : "div"}></div>`).join("")}${modelCallsHTML(e)}${subagentLinks(e.subagents)}${eventSkillsHTML(e.skills)}${
              p.fields.length
                ? `<dl class="fields">${p.fields
                    .filter(([k]) => k !== "Result")
                    .map(
                      ([k, v]) =>
                        `<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`,
                    )
                    .join("")}</dl>`
                : ""
            }</div></article>`,
        )
        .join("")
    : '<p class="empty">No matching events on this page</p>';
  if (inspectedID && !visible.some(([e]) => e.id === inspectedID))
    closeInspector(false);
  document.querySelectorAll("[data-event]").forEach((card) => {
    card.classList.toggle("selected", card.dataset.event === inspectedID);
    card.onclick = (event) => {
      if (
        !event.target.closest("a,button") &&
        !window.getSelection()?.toString()
      )
        openInspector(card.dataset.event);
    };
    card.onkeydown = (e) => {
      if (e.target !== card) return;
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        openInspector(card.dataset.event);
      }
    };
  });
  bindModelCalls($("#timeline"));
  bindSubagentLinks($("#timeline"));
  bindSubagentLinks($("#relations"));
  if (focusedID)
    document
      .querySelector(`[data-event="${CSS.escape(focusedID)}"]`)
      ?.focus({ preventScroll: true });
  if (inspectedID) renderInspector();
  updateTimelineNavigation();
}

function setInspectorTab(tab) {
  inspectorTab = tab;
  if (tab === "json") renderInspectorJSON();
  for (const name of ["details", "json"]) {
    const button = $(name === "details" ? "#detailtab" : "#jsontab");
    button.setAttribute("aria-selected", String(name === tab));
    button.tabIndex = name === tab ? 0 : -1;
    $("#inspect" + name).hidden = name !== tab;
  }
}
function syncInspectorMode() {
  const modal =
    !!(inspectedID || subagentState) &&
    matchMedia("(max-width: 900px)").matches;
  $("#inspector").setAttribute("role", modal ? "dialog" : "region");
  if (modal) $("#inspector").setAttribute("aria-modal", "true");
  else $("#inspector").removeAttribute("aria-modal");
  document.body.classList.toggle("inspector-modal", modal);
  for (const el of [...$("#detailpage").children].filter(
    (x) => !x.classList.contains("eventlayout"),
  ))
    el.inert = modal;
  $("#eventlist").inert = modal;
  $(".topbar").inert = modal;
  if (modal && !$("#inspector").contains(document.activeElement))
    $("#closeinspector").focus({ preventScroll: true });
}
async function openInspector(id) {
  resetSubagentPreview();
  inspectorTrigger = document.querySelector(
    `#timeline [data-event="${CSS.escape(id)}"]`,
  );
  inspectorRequest?.abort();
  const controller = new AbortController();
  inspectorRequest = controller;
  inspectedID = id;
  inspectorEvent = inspectorCache.get(id) || null;
  inspectedRecord = null;
  jsonEvent = null;
  $("#eventjson").textContent = "";
  if (!inspectorEvent && !events.some((event) => event.id === id)) {
    delete $("#inspector").dataset.kind;
    delete $("#inspector").dataset.error;
    $("#inspectdetails").textContent = "";
    $("#inspectortitle").textContent = "Event details";
    $("#inspectortime").textContent = "";
    setInspectorTab(inspectorTab);
  }
  $("#inspector").hidden = false;
  $("#detailpage").classList.add("inspector-open");
  document.querySelectorAll("[data-event]").forEach((card) => {
    const active = card.dataset.event === id;
    card.classList.toggle("selected", active);
    card.setAttribute("aria-expanded", String(active));
  });
  $("#inspectstatus").textContent = inspectorEvent ? "" : "Loading details…";
  $("#copyjson").disabled = !inspectorEvent;
  renderInspector();
  syncInspectorMode();
  $("#closeinspector").focus({ preventScroll: true });
  if (inspectorEvent) return;
  try {
    const event = await api(
      `/api/event?session=${encodeURIComponent(selected)}&id=${encodeURIComponent(id)}&origin=${encodeURIComponent(pageOrigin)}`,
      controller.signal,
    );
    if (controller.signal.aborted || inspectedID !== id) return;
    inspectorCache.clear();
    inspectorCache.set(id, event);
    inspectorEvent = event;
    $("#inspectstatus").textContent = "";
    $("#copyjson").disabled = false;
    renderInspector();
  } catch (e) {
    if (e.name !== "AbortError" && inspectedID === id) {
      $("#inspectstatus").textContent = e.message + " ";
      const retry = document.createElement("button");
      retry.textContent = "Retry";
      retry.onclick = () => openInspector(id);
      $("#inspectstatus").append(retry);
    }
  }
}
function renderInspectorJSON() {
  if (!inspectorEvent) {
    $("#eventjson").textContent = "Loading JSON…";
    return;
  }
  if (jsonEvent === inspectorEvent) return;
  jsonEvent = inspectorEvent;
  $("#eventjson").textContent = JSON.stringify(
    inspectorEvent.records.length === 1
      ? inspectorEvent.records[0].raw
      : { records: inspectorEvent.records },
    null,
    2,
  );
}
function closeInspector(restore = true) {
  resetSubagentPreview();
  inspectorRequest?.abort();
  const id = inspectedID;
  inspectedID = "";
  inspectedRecord = null;
  inspectorEvent = null;
  jsonEvent = null;
  $("#inspector").hidden = true;
  $("#detailpage").classList.remove("inspector-open");
  syncInspectorMode();
  document.querySelectorAll("[data-event]").forEach((card) => {
    card.classList.remove("selected");
    card.setAttribute("aria-expanded", "false");
  });
  if (restore)
    (inspectorTrigger?.isConnected
      ? inspectorTrigger
      : id
        ? document.querySelector(`[data-event="${CSS.escape(id)}"]`)
        : null
    )?.focus({ preventScroll: true });
  inspectorTrigger = null;
}
function renderInspector() {
  if (subagentState) return;
  const e = inspectorEvent || events.find((e) => e.id === inspectedID);
  if (!e || e === inspectedRecord) return;
  inspectedRecord = e;
  const parent = events.find(
    (x) =>
      x.span === e.parent_span &&
      x.trace === e.trace &&
      x.source === e.source &&
      document.getElementById("span-" + x.span),
  );
  const p = e.detail,
    value = (v) => (typeof v === "string" ? v : JSON.stringify(v, null, 2));
  $("#inspector").dataset.kind = p.kind;
  $("#inspector").dataset.error = String(p.error);
  $("#inspectortitle").textContent = eventTitle(p);
  $("#inspectortime").textContent =
    date(e.time) +
    " · " +
    label(e.source) +
    (p.kind === "tool" ? " · " + toolPhaseLabel(p) : "");
  const properties = [
    [
      "Sources",
      e.origins.map((x) => (x === "import" ? "Imported" : "OTEL")).join(" + "),
    ],
    ["Signal", e.signal],
    ["Time", e.time],
    ["Event", p.name],
    ["Trace ID", e.trace],
    ["Span ID", e.span],
    ["Parent span", e.parent_span],
    ["Session", e.session],
    ...p.fields,
  ].filter(([, v]) => v !== undefined && v !== "");
  $("#inspectdetails").innerHTML =
    modelCallsHTML(e) +
    subagentLinks(e.subagents) +
    eventSkillsHTML(e.skills) +
    toolPairHTML(e) +
    p.blocks
      .map(
        (b) =>
          `<div class="block ${b.label === "Error" ? "failure-detail" : ""}"><div class="blocklabel">${esc(blockLabel(b, p))}</div><${b.code ? "pre" : "div"} class="${b.code ? "code" : "prose"}">${esc(b.text)}</${b.code ? "pre" : "div"}></div>`,
      )
      .join("") +
    `<h3>Properties</h3><dl class="propertylist">${properties.map(([k, v]) => `<div><dt>${esc(k)}</dt><dd>${k === "Parent span" && parent ? `<a href="#span-${esc(parent.span)}" id="inspectparent">${esc(value(v))}</a>` : esc(value(v))}</dd></div>`).join("")}</dl>` +
    (p.conflicts?.length
      ? `<h3>Source differences</h3><dl class="propertylist">${p.conflicts.map((c) => `<div><dt>${esc(c.field)}</dt><dd>${c.values.map((v) => `${v.origin === "import" ? "Imported" : "OTEL"}: ${esc(v.value)}`).join("\n")}</dd></div>`).join("")}</dl>`
      : "") +
    `<h3>Attributes</h3><dl class="propertylist">${
      Object.entries(p.attributes)
        .map(
          ([k, v]) => `<div><dt>${esc(k)}</dt><dd>${esc(value(v))}</dd></div>`,
        )
        .join("") || "<div>No attributes</div>"
    }</dl>`;
  if ($("#inspectparent"))
    $("#inspectparent").onclick = (ev) => {
      ev.preventDefault();
      openInspector(parent.id);
      document
        .getElementById("span-" + parent.span)
        ?.scrollIntoView({ block: "nearest" });
    };
  bindModelCalls($("#inspectdetails"));
  bindSubagentLinks($("#inspectdetails"));
  bindToolPairs($("#inspectdetails"));
  $("#copystatus").textContent = "";
  setInspectorTab(inspectorTab);
}
$("#closeinspector").onclick = () => closeInspector();
$("#detailtab").onclick = () => setInspectorTab("details");
$("#jsontab").onclick = () => setInspectorTab("json");
$(".inspectortabs").onkeydown = (e) => {
  if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) {
    e.preventDefault();
    setInspectorTab(
      e.key === "Home"
        ? "details"
        : e.key === "End"
          ? "json"
          : inspectorTab === "json"
            ? "details"
            : "json",
    );
    $(inspectorTab === "json" ? "#jsontab" : "#detailtab").focus();
  }
};
$("#copyjson").onclick = async () => {
  try {
    await navigator.clipboard.writeText($("#eventjson").textContent);
    $("#copystatus").textContent = "Copied";
  } catch {
    $("#copystatus").textContent = "Select the JSON to copy";
  }
};
addEventListener("resize", syncInspectorMode);
addEventListener("keydown", (e) => {
  if (!inspectedID && !subagentState) return;
  if (e.key === "Escape") {
    e.preventDefault();
    closeInspector();
    return;
  }
  if (e.key === "Tab" && matchMedia("(max-width: 900px)").matches) {
    const items = [
      ...$("#inspector").querySelectorAll('button,a[href],[tabindex="0"]'),
    ].filter(
      (el) => el.getClientRects().length && !el.disabled && el.tabIndex >= 0,
    );
    const first = items[0],
      last = items.at(-1);
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }
});
function updateTimelineNavigation(
  loading = $("#workspace").hasAttribute("aria-busy"),
) {
  if (meta)
    eventPager.update({
      total: meta.count,
      offset: meta.offset,
      size: meta.limit,
    });
  eventPager.setLoading(loading);
  $("#origin").disabled = loading;
}
function captureTimelinePosition() {
  const card = [...document.querySelectorAll("[data-event]")].find((el) => {
    const r = el.getBoundingClientRect();
    return r.bottom > 0 && r.top < innerHeight;
  });
  return {
    id: card?.dataset.event,
    top: card?.getBoundingClientRect().top,
    y: scrollY,
  };
}
function placeTimeline(mode, anchor, serial) {
  requestAnimationFrame(() => {
    if (serial !== requestSerial) return;
    if (mode === "bottom")
      window.scrollTo({
        top: document.documentElement.scrollHeight,
        behavior: "instant",
      });
    else if (mode === "top")
      $("#pageinfo").scrollIntoView({ block: "start", behavior: "instant" });
    else {
      const card = anchor?.id
        ? document.querySelector(`[data-event="${CSS.escape(anchor.id)}"]`)
        : null;
      if (card)
        window.scrollBy(0, card.getBoundingClientRect().top - anchor.top);
      else if (anchor) window.scrollTo(0, anchor.y);
    }
  });
}
async function refresh(scrollMode) {
  let anchor = null;
  activeRequest?.abort();
  const controller = new AbortController();
  activeRequest = controller;
  const serial = ++requestSerial;
  $("#workspace").setAttribute("aria-busy", "true");
  for (const pager of [sessionPager, eventPager, archivePager, skillsPager])
    pager.setLoading(true);
  $("#origin").disabled = true;
  if (exportMode) $("#downloadexport").disabled = true;
  try {
    if (skillsMode) {
      await Promise.all([
        refreshSkills(controller.signal),
        loadProjects(controller.signal),
      ]);
      if (!controller.signal.aborted && scrollMode === "top")
        window.scrollTo({ top: 0, behavior: "instant" });
    } else if (archiveMode) {
      await refreshArchive(controller.signal);
      if (!controller.signal.aborted && scrollMode === "top")
        window.scrollTo({ top: 0, behavior: "instant" });
    } else if (selected) {
      const latest = eventOffset === null;
      const paging = latest
        ? "&position=latest"
        : `&offset=${eventOffset}&limit=${eventLimit}`;
      const page = await api(
        `/api/session?id=${encodeURIComponent(selected)}&origin=${encodeURIComponent(origin)}&view=${view}&kind=${kind}${paging}`,
        controller.signal,
      );
      if (controller.signal.aborted) return;
      anchor = captureTimelinePosition();
      scrollMode ??= latest ? "bottom" : meta ? "preserve" : "top";
      closeInspector(false);
      inspectorCache.clear();
      meta = page;
      pageOrigin = origin;
      events = page.events;
      eventOffset = page.offset;
      eventLimit = page.limit;
      detailState(latest);
      renderEvents();
    } else {
      const params = listParameters();
      params.set("offset", sessionOffset);
      if (folder !== null) params.set("folder", folder);
      const [response] = await Promise.all([
          apiResponse((exportMode ? "/api/export/sessions?" : "/api/sessions?") + params, controller.signal),
          loadProjects(controller.signal),
        ]),
        data = await response.json();
      if (controller.signal.aborted) return;
      sessionOffset = Number(response.headers.get("X-Page-Offset"));
      sessionData = {
        offset: sessionOffset,
        query,
        idKey,
        idValue,
        folder,
        project,
        projectSelections: [...projectSelections],
        searchScope: structuredClone(searchScope),
        diagnostics: $("#showdiagnostics").checked,
        exportChildren: $("#exportchildren").checked,
        skillFilter,
        skillPlugin,
      };
      listState();
      renderSessions(data, Number(response.headers.get("X-Total-Count")));
      refreshSearchAnalysis(controller.signal);
      if (exportMode) {
        $("#exportrecords").textContent = Number(response.headers.get("X-Record-Count")).toLocaleString() + " records";
        const download = new URLSearchParams(params);
        download.delete("offset");
        exportDownloadURL = "/api/export/download?" + download;
        $("#downloadexport").disabled = exportQueryPending || !Number(response.headers.get("X-Total-Count"));
      }
      if (scrollMode === "top")
        window.scrollTo({ top: 0, behavior: "instant" });
    }
    if (controller.signal.aborted) return;
    $("#login").hidden = true;
    $("#workspace").hidden = false;
    $("#listpage").hidden = !!selected || archiveMode || skillsMode;
    $("#skillpage").hidden = !skillsMode;
    $("#detailpage").hidden = !selected || archiveMode || skillsMode;
    $("#archivepage").hidden = !archiveMode;
    updateDocumentTitle();
    $("#error").textContent = "";
    if (selected) placeTimeline(scrollMode, anchor, serial);
  } catch (e) {
    if (e.name === "AbortError") return;
    if (selected && meta) {
      eventOffset = meta.offset;
      eventLimit = meta.limit;
      origin = pageOrigin;
      view = meta.view || "activity";
      kind = meta.kind || "";
      syncEventFilters();
      $("#origin").value = origin;
    } else if (skillsMode && skillsData) {
      skillsQuery = skillsData.query;
      skillsOffset = skillsData.offset;
      skillsSort = skillsData.sort;
      project = skillsData.project;
      projectSelections = skillsData.projectSelections;
      searchScope = skillsData.searchScope;
      renderSearchControls();
      $("#skillsort").value = skillsSort;
      renderProjects();
      $("#skillsearch").value = skillsQuery;
    } else if (archiveMode && archiveData) {
      archiveOffset = archiveData.offset;
    } else if (sessionData) {
      ({
        offset: sessionOffset,
        query,
        idKey,
        idValue,
        folder,
        project,
        projectSelections,
        searchScope,
        skillFilter,
        skillPlugin,
      } = sessionData);
      $("#showdiagnostics").checked = sessionData.diagnostics;
      $("#exportchildren").checked = sessionData.exportChildren;
      renderSearchControls();
      $("#search").value = query;
      syncIdentifierSearch();
      listState();
      renderProjects();
    }
    $("#error").textContent = e.message;
    if ($("#login").hidden) {
      $("#workspace").hidden = false;
      $("#listpage").hidden = !!selected || archiveMode || skillsMode;
      $("#skillpage").hidden = !skillsMode;
      $("#detailpage").hidden = !selected || archiveMode || skillsMode;
      $("#archivepage").hidden = !archiveMode;
    }
  } finally {
    if (serial === requestSerial) {
      $("#workspace").removeAttribute("aria-busy");
      for (const pager of [sessionPager, eventPager, archivePager, skillsPager])
        pager.setLoading(false);
      $("#origin").disabled = false;
    }
  }
}

$("#loginform").onsubmit = async (e) => {
  e.preventDefault();
  try {
    const r = await fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: $("#token").value }),
    });
    if (!r.ok) throw Error("Invalid access token.");
    $("#token").value = "";
    refresh();
  } catch (e) {
    $("#loginerror").textContent = e.message;
  }
};
$("#logout").onclick = async () => {
  await fetch("/api/logout", { method: "POST" });
  location.reload();
};
function syncEventFilters() {
  document
    .querySelectorAll("button[data-kind]")
    .forEach((b) => b.classList.toggle("active", b.dataset.kind === kind));
  document.querySelectorAll("[data-view]").forEach((b) => {
    b.classList.toggle("active", b.dataset.view === view);
    b.setAttribute("aria-pressed", String(b.dataset.view === view));
  });
}
document.querySelectorAll("button[data-kind]").forEach(
  (b) =>
    (b.onclick = () => {
      kind = b.dataset.kind;
      if (kind === "metric") view = "diagnostics";
      eventOffset = null;
      eventLimit = 200;
      syncEventFilters();
      closeInspector(false);
      refresh("bottom");
    }),
);
document.querySelectorAll("[data-view]").forEach(
  (b) =>
    (b.onclick = () => {
      view = b.dataset.view;
      if (view === "activity" && kind === "metric") kind = "";
      eventOffset = null;
      eventLimit = 200;
      syncEventFilters();
      closeInspector(false);
      refresh("bottom");
    }),
);
syncEventFilters();
setupProjects();
$("#downloadexport").onclick = () => {
  if (exportDownloadURL) location.assign(exportDownloadURL);
};
$("#exportchildren").onchange = () => { sessionOffset = 0; refresh(); };

function syncIdentifierSearch() {
  $("#identifierfield").value = idKey;
  $("#identifiervalue").value = idValue;
  $("#idfilterstatus").textContent = idValue ? " · Active" : "";
  if (idValue) $("#idsearch").open = true;
}
$("#idsearchform").onsubmit = (e) => {
  e.preventDefault();
  idKey = $("#identifierfield").value.trim();
  idValue = $("#identifiervalue").value;
  sessionOffset = 0;
  syncIdentifierSearch();
  listState();
  refresh();
};
$("#clearidsearch").onclick = () => {
  idKey = idValue = "";
  sessionOffset = 0;
  syncIdentifierSearch();
  listState();
  refresh();
};
let timer,
  composingSearch = false;
function scheduleSearch() {
  clearTimeout(timer);
  if (exportMode) {
    exportQueryPending = true;
    $("#downloadexport").disabled = true;
  }
  timer = setTimeout(() => {
    exportQueryPending = false;
    query = $("#search").value;
    sessionOffset = 0;
    listState();
    refresh();
  }, 250);
}
$("#showdiagnostics").onchange = () => {
  sessionOffset = 0;
  listState();
  refresh();
};
$("#search").addEventListener("compositionstart", () => {
  composingSearch = true;
  clearTimeout(timer);
  if (exportMode) {
    exportQueryPending = true;
    $("#downloadexport").disabled = true;
  }
});
$("#search").addEventListener("compositionend", () => {
  composingSearch = false;
  scheduleSearch();
});
$("#search").oninput = (e) => {
  if (!composingSearch && !e.isComposing) scheduleSearch();
};
const sessionPager = new Pagination($("#sessionpagination"), {
  noun: "sessions",
  onChange: (page) => {
    sessionOffset = (page - 1) * 200;
    refresh("top");
  },
});
const eventPager = new Pagination($("#eventpagination"), {
  noun: "events",
  summary: () => (meta ? usageShortcut(meta.usage) : ""),
  onChange: (page) => {
    eventLimit = 200;
    eventOffset = page === eventPager.pages ? null : (page - 1) * 200;
    refresh(page === eventPager.pages ? "bottom" : "top");
  },
  onLatest: () => {
    eventOffset = null;
    eventLimit = 200;
    refresh("bottom");
  },
});
$("#eventpagination").addEventListener("click", (event) => {
  if (event.target.closest(".usage-shortcut")) {
    event.preventDefault();
    $("#sessionusage").scrollIntoView({ block: "start" });
    $("#sessionusage").focus({ preventScroll: true });
  }
});
const archivePager = new Pagination($("#archivepagination"), {
  noun: "folders",
  onChange: (page) => {
    archiveOffset = (page - 1) * 200;
    refresh("top");
  },
});
$("#origin").onchange = () => {
  origin = $("#origin").value;
  eventOffset = null;
  eventLimit = 200;
  closeInspector(false);
  refresh("bottom");
};
setupSearchControls();
setupSubagentPreview();
setupSkills();
setupArchive();
refresh();
