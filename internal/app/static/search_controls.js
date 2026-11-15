"use strict";
function readSearchScope(params) {
  return {
    from: params.get("from") || "", to: params.get("to") || "",
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
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 23);
}
function renderSearchControls() {
  $("#searchfrom").value = localDateValue(searchScope.from);
  $("#searchto").value = localDateValue(searchScope.to);
  $("#searchdates").hidden = searchScope.range === "all";
  $("#searchrange").value = searchScope.range;
  for (const selector of ["#searchfrom", "#searchto"]) $(selector).dataset.display = $(selector).value;
  $("#textmatch").value = searchScope.textMode;
  $("#searchexclude").value = searchScope.exclude;
  for (const [name, values] of [["searchsource", searchScope.sources], ["searchorigin", searchScope.origins], ["searchkind", searchScope.kinds]])
    document.querySelectorAll(`input[name=${name}]`).forEach((el) => { el.checked = values.includes(el.value); });
  const n = searchScope.sources.length + searchScope.origins.length + (skillsMode ? 0 : searchScope.kinds.length) + Number(!!searchScope.exclude) + Number(searchScope.textMode !== "all");
  $("#facetcount").textContent = n ? " · " + n : "";
  $("#searchfiltererror").textContent = "";
}
function applySearchControls() {
  const readBound = (selector, current) => {
    const input = $(selector);
    if (!input.reportValidity()) throw Error("Check the date and time.");
    if (input.value === input.dataset.display) return current;
    return input.value ? new Date(input.value).toISOString() : "";
  };
  try {
    const from = readBound("#searchfrom", searchScope.from), to = readBound("#searchto", searchScope.to);
    if (from && to && new Date(from) >= new Date(to)) throw Error("From must be earlier than To.");
    const checked = (name) => [...document.querySelectorAll(`input[name=${name}]:checked`)].map((el) => el.value);
    searchScope = { from, to, range: from === searchScope.from && to === searchScope.to ? searchScope.range : from || to ? "custom" : "all", sources: checked("searchsource"), origins: checked("searchorigin"), kinds: skillsMode ? [] : checked("searchkind"), textMode: $("#textmatch").value, exclude: $("#searchexclude").value.trim() };
    searchScopeChanged();
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
  const host = skillsMode ? $(".skill-results .toolbar") : $(".session-results .toolbar");
  host.after($("#searchcontrols"));
  $("#searchtimezone").textContent = Intl.DateTimeFormat().resolvedOptions().timeZone;
  $("#eventtypefilters").hidden = skillsMode;
  renderSearchControls();
  $("#applysearchfilters").onclick = applySearchControls;
  $("#searchrange").onchange = () => {
    const value = $("#searchrange").value;
    if (value === "custom") { searchScope.range = "custom"; $("#searchdates").hidden = false; $("#searchfrom").focus(); return; }
    const now = new Date();
    searchScope.range = value;
    searchScope.from = value === "all" ? "" : new Date(now.getTime() - Number(value) * 1000).toISOString();
    searchScope.to = value === "all" ? "" : now.toISOString();
    renderSearchControls();
    searchScopeChanged();
  };
  for (const selector of ["input[name=searchsource]", "input[name=searchorigin]", "input[name=searchkind]"])
    document.querySelectorAll(selector).forEach((input) => { input.onchange = applySearchControls; });
  $("#resetsearchfilters").onclick = () => {
    searchScope = readSearchScope(new URLSearchParams());
    project = null; projectSelections = []; folder = null;
    idKey = idValue = skillFilter = skillPlugin = query = skillsQuery = "";
    $("#search").value = $("#skillsearch").value = "";
    $("#showdiagnostics").checked = exportMode;
    if (exportMode) $("#exportchildren").checked = true;
    syncIdentifierSearch();
    searchScopeChanged();
  };
  for (const input of [$("#searchexclude"), $("#searchfrom"), $("#searchto")]) input.onkeydown = (e) => { if (e.key === "Enter" && !e.isComposing) { e.preventDefault(); applySearchControls(); } };
  $("#textmatch").onchange = applySearchControls;
  $("#searchanalysis").hidden = skillsMode || exportMode;
  $("#searchanalysis").ontoggle = () => { if ($("#searchanalysis").open) refreshSearchAnalysis(); else analysisRequest?.abort(); };
  $("#analysisgroup").onchange = () => refreshSearchAnalysis();
}
let analysisRequest = null;
async function refreshSearchAnalysis(signal) {
  if (!$("#searchanalysis").open || selected || skillsMode || archiveMode || exportMode) return;
  analysisRequest?.abort();
  const controller = new AbortController(); analysisRequest = controller;
  signal?.addEventListener("abort", () => controller.abort(), { once: true });
  const params = listParameters();
  if (folder !== null) params.set("folder", folder);
  params.set("group", $("#analysisgroup").value);
  $("#analysisstatus").textContent = "Loading…";
  $("#analysisrows").replaceChildren();
  const exportParams = new URLSearchParams(params);
  exportParams.delete("group");
  exportParams.set("diagnostics", $("#showdiagnostics").checked ? "1" : "0");
  exportParams.set("include_children", "0");
  $("#exportmatches").href = "/export?" + exportParams;
  try {
    const data = await api("/api/search/summary?" + params, controller.signal);
    if (controller.signal.aborted) return;
    $("#analysisstatus").textContent = data.limited ? "Showing 100 groups; narrow the time range." : data.groups.length ? "" : "No matching records";
    $("#analysisrows").innerHTML = data.groups.map((b) => `<tr><th scope="row">${esc(data.group === "project" ? projectName(b.key) : data.group === "source" ? label(b.key) : b.key)}</th><td>${b.records.toLocaleString()}</td><td>${b.sessions.toLocaleString()}</td><td>${b.errors.toLocaleString()}</td></tr>`).join("");
  } catch (e) { if (e.name !== "AbortError") $("#analysisstatus").textContent = e.message; }
}
