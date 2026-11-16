"use strict";
function searchSecond(value) {
  if (!value) return "";
  const d = new Date(value);
  return Number.isFinite(d.getTime()) ? new Date(Math.floor(d.getTime() / 1000) * 1000).toISOString().replace(".000Z", "Z") : value;
}
function readSearchScope(params) {
  return {
    from: searchSecond(params.get("from")), to: searchSecond(params.get("to")),
    range: params.has("from") || params.has("to") ? "custom" : "all",
    sources: [...new Set(params.getAll("source").filter(Boolean))],
    origins: [...new Set(params.getAll("origin"))],
    kinds: [...new Set(params.getAll("event_kind"))],
    textMode: params.get("text_mode") || "all", exclude: params.get("exclude") || "",
  };
}
function selectedProjects() {
  return projectSelections.length ? projectSelections : project === null ? [] : [project];
}
function projectScopeName() {
  const ids = selectedProjects();
  return ids.length > 1 ? ids.length + " projects" : ids.length ? projectName(ids[0]) : "";
}
function appendSearchScope(params, { projects = true, kinds = true, text = true } = {}) {
  if (projects) for (const id of selectedProjects()) params.append("project", id);
  for (const [key, values] of [["source", searchScope.sources], ["origin", searchScope.origins], ["event_kind", kinds ? searchScope.kinds : []]])
    for (const value of values) params.append(key, value);
  if (searchScope.from) params.set("from", searchScope.from);
  if (searchScope.to) params.set("to", searchScope.to);
  if (text && searchScope.textMode !== "all") params.set("text_mode", searchScope.textMode);
  if (text && searchScope.exclude) params.set("exclude", searchScope.exclude);
  return params;
}
function localDateValue(value) {
  if (!value) return "";
  const d = new Date(value);
  if (!Number.isFinite(d.getTime())) return "";
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 19);
}
function renderSearchOptionStatus() {
  $("#searchoptionstatus").textContent = searchScope.exclude || searchScope.textMode !== "all" || !skillsMode && idValue ? " · Active" : "";
}
function applyTextOptions() {
  searchScope.textMode = $("#textmatch").value;
  searchScope.exclude = $("#searchexclude").value.trim();
}
function renderSearchControls() {
  $("#showdiagnostics").checked = includeInternalSessions;
  $("#searchfrom").value = localDateValue(searchScope.from);
  $("#searchto").value = localDateValue(searchScope.to);
  $("#searchdates").hidden = searchScope.range === "all";
  $("#searchrange").value = searchScope.range;
  for (const selector of ["#searchfrom", "#searchto"]) $(selector).dataset.display = $(selector).value;
  $("#textmatch").value = searchScope.textMode;
  $("#searchexclude").value = searchScope.exclude;
  for (const [name, values] of [["searchsource", searchScope.sources], ["searchorigin", searchScope.origins], ["searchkind", searchScope.kinds]])
    document.querySelectorAll(`input[name=${name}]`).forEach((el) => { el.checked = values.includes(el.value); });
  const n = searchScope.sources.length + searchScope.origins.length + (skillsMode ? 0 : searchScope.kinds.length);
  $("#facetcount").textContent = n ? " · " + n : "";
  renderSearchOptionStatus();
  const timeLabel = (value) => new Date(value).toLocaleString(undefined, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  $("#searchtimelabel").textContent = searchScope.range === "all" ? "All time" : searchScope.range !== "custom" ? $("#searchrange").selectedOptions[0].textContent : [searchScope.from ? timeLabel(searchScope.from) : "Any start", searchScope.to ? timeLabel(searchScope.to) : "Any end"].join(" – ");
  $("#searchtime summary").title = $("#searchtimelabel").textContent;
  $("#searchfiltererror").textContent = "";
}
function applySearchControls({ time = false } = {}) {
  const readBound = (selector, current) => {
    const input = $(selector);
    if (!input.reportValidity()) throw Error("Check the date and time.");
    if (input.value === input.dataset.display) return current;
    return input.value ? searchSecond(input.value) : "";
  };
  try {
    const from = time ? readBound("#searchfrom", searchScope.from) : searchScope.from, to = time ? readBound("#searchto", searchScope.to) : searchScope.to;
    if (from && to && new Date(from) >= new Date(to)) throw Error("From must be earlier than To.");
    const checked = (name) => [...document.querySelectorAll(`input[name=${name}]:checked`)].map((el) => el.value);
    searchScope = { from, to, range: from === searchScope.from && to === searchScope.to ? searchScope.range : from || to ? "custom" : "all", sources: checked("searchsource"), origins: checked("searchorigin"), kinds: skillsMode ? [] : checked("searchkind"), textMode: searchScope.textMode, exclude: searchScope.exclude };
    searchScopeChanged();
    if (time) { $("#searchtime").open = false; $("#searchtime summary").focus({ preventScroll: true }); }
  } catch (e) { $("#searchfiltererror").textContent = e.message; }
}
function searchScopeChanged() {
  sessionOffset = skillsOffset = 0;
  renderSearchControls();
  if (!skillsMode) listState();
  refresh();
}
function projectCheckbox(id, checked) {
  const ids = new Set(selectedProjects());
  if (checked) ids.add(id); else ids.delete(id);
  projectSelections = [...ids];
  project = ids.size === 1 ? [...ids][0] : null;
  if (ids.size <= 1) projectSelections = [];
  folder = null;
  renderProjects();
  searchScopeChanged();
}
function setupSearchControls() {
  const main = skillsMode ? $("#skillpage") : $("#listpage");
  main.prepend($("#searchcontrols"));
  $("#queryhost").append(skillsMode ? $("#skillsearch") : $("#search"));
  $("#sessionqueryhost").hidden = true;
  $(".identifier-fields").hidden = skillsMode;
  $("#showdiagnostics").closest("label").hidden = skillsMode;
  const narrowScreen = matchMedia("(max-width: 720px)");
  $("#facetoptions").open = !narrowScreen.matches;
  narrowScreen.addEventListener("change", (event) => { $("#facetoptions").open = !event.matches; });
  $("#searchtimezone").textContent = Intl.DateTimeFormat().resolvedOptions().timeZone;
  $("#eventtypefilters").hidden = skillsMode;
  renderSearchControls();
  $("#applysearchfilters").onclick = () => applySearchControls({ time: true });
  $("#searchrange").onchange = () => {
    const value = $("#searchrange").value;
    if (value === "custom") { $("#searchdates").hidden = false; $("#searchfrom").focus(); return; }
    const now = new Date();
    searchScope.range = value;
    searchScope.from = value === "all" ? "" : searchSecond(new Date(now.getTime() - Number(value) * 1000));
    searchScope.to = value === "all" ? "" : searchSecond(now);
    renderSearchControls();
    searchScopeChanged();
    $("#searchtime").open = false;
    $("#searchtime summary").focus({ preventScroll: true });
  };
  for (const selector of ["input[name=searchsource]", "input[name=searchorigin]", "input[name=searchkind]"])
    document.querySelectorAll(selector).forEach((input) => { input.onchange = () => applySearchControls(); });
  $("#resetsearchfilters").onclick = () => {
    searchScope = readSearchScope(new URLSearchParams());
    project = null; projectSelections = []; folder = null;
    idKey = idValue = skillFilter = skillPlugin = query = skillsQuery = "";
    $("#search").value = $("#skillsearch").value = "";
    includeInternalSessions = false;
    syncIdentifierSearch();
    searchScopeChanged();
    $("#searchtime").open = $("#searchoptions").open = false;
  };
  for (const input of [$("#searchfrom"), $("#searchto")]) input.onkeydown = (e) => { if (e.key === "Enter" && !e.isComposing) { e.preventDefault(); applySearchControls({ time: input.type === "datetime-local" }); } };
  // Native disclosure widgets keep keyboard support, and outside/Escape closes overlays.
  const overlays = [$("#searchtime"), $("#searchoptions"), $("#dataoptions")];
  document.addEventListener("click", (event) => overlays.forEach((details) => { if (!details.contains(event.target)) details.open = false; }));
  document.addEventListener("keydown", (event) => { if (event.key === "Escape") for (const details of overlays) if (details.open) { details.open = false; if (details.contains(document.activeElement)) details.querySelector("summary").focus(); } });
  $("#searchanalysis").hidden = skillsMode;
  $("#searchanalysis").ontoggle = () => { if ($("#searchanalysis").open) refreshSearchAnalysis(); else analysisRequest?.abort(); };
  $("#analysisgroup").onchange = () => refreshSearchAnalysis();
}
let analysisRequest = null;
async function refreshSearchAnalysis(signal) {
  if (!$("#searchanalysis").open || selected || skillsMode) return;
  analysisRequest?.abort();
  const controller = new AbortController(); analysisRequest = controller;
  signal?.addEventListener("abort", () => controller.abort(), { once: true });
  const params = listParameters();
  if (folder !== null) params.set("folder", folder);
  params.set("group", $("#analysisgroup").value);
  $("#analysisstatus").textContent = "Loading…";
  $("#analysisrows").replaceChildren();
  try {
    const data = await api("/api/search/summary?" + params, controller.signal);
    if (controller.signal.aborted) return;
    $("#analysisstatus").textContent = data.limited ? "Showing 100 groups; narrow the time range." : data.groups.length ? "" : "No matching records";
    $("#analysisrows").innerHTML = data.groups.map((b) => `<tr><th scope="row">${esc(data.group === "project" ? projectName(b.key) : data.group === "source" ? label(b.key) : b.key)}</th><td>${b.records.toLocaleString()}</td><td>${b.sessions.toLocaleString()}</td><td>${b.errors.toLocaleString()}</td></tr>`).join("");
  } catch (e) { if (e.name !== "AbortError") $("#analysisstatus").textContent = e.message; }
}
