"use strict";
let skillsQuery = "",
  skillsSort = "uses",
  skillsOffset = 0,
  skillsData = null,
  skillsPager = null;
function skillSessionURL(name, plugin) {
  const params = new URLSearchParams({ skill: name, plugin: plugin || "" });
  appendSearchScope(params, { kinds: false, text: false });
  return "/?" + params;
}
function skillUsesLabel(row) {
  return (row.inferred ? "≈" : "") + row.uses.toLocaleString();
}
function skillUsesTitle(row) {
  return (row.uses - row.inferred).toLocaleString() + " reported; " +
    row.inferred.toLocaleString() + " inferred from sessions with skill reads or injections. Repeat reads count once per session.";
}
function sessionSkillsHTML(rows) {
  return rows?.length
    ? `<span class="muted">Skills</span>${rows.map((row) => `<button type="button" class="session-skill" data-skill-event="${esc(row.event)}" title="${esc(skillUsesTitle(row))} · View latest skill event">${esc(row.name)} <small>${row.inferred ? skillUsesLabel(row) : "×" + skillUsesLabel(row)}</small></button>`).join("")}<a class="skill-browse" href="/skills">Browse skills</a>`
    : "";
}
function eventSkillsHTML(rows) {
  return rows?.length
    ? `<div class="event-skills">${rows.map((row) => `<a href="${esc(skillSessionURL(row.name, row.plugin))}">${row.evidence === "reported" ? "Skill" : "Skill reference"}: ${esc(row.name)}</a>`).join("")}</div>`
    : "";
}
function bindSkillEvents(root) {
  root.querySelectorAll("[data-skill-event]").forEach(
    (button) =>
      (button.onclick = () => {
        openInspector(button.dataset.skillEvent);
        inspectorTrigger = button;
      }),
  );
}
async function refreshSkills(signal) {
  const params = new URLSearchParams({
    offset: String(skillsOffset),
    sort: skillsSort,
  });
  appendSearchScope(params, { kinds: false });
  if (skillsQuery) params.set("q", skillsQuery);
  const data = await api("/api/skills?" + params, signal);
  if (signal?.aborted) return;
  skillsData = { ...data, query: skillsQuery, project, projectSelections: [...projectSelections], searchScope: structuredClone(searchScope) };
  skillsOffset = data.offset;
  $("#skilltitle").textContent =
    selectedProjects().length ? projectScopeName() + " · Skills" : "Skills";
  $("#skillcount").textContent =
    data.total.toLocaleString() +
    (data.total === 1 ? " skill · " : " skills · ") +
    skillUsesLabel(data) +
    (data.uses === 1 ? " use" : " uses");
  $("#skillcount").title = skillUsesTitle(data);
  $("#skillindexstatus").textContent = data.ready ? "" : "Indexing history";
  $("#skillrows").innerHTML = data.skills.length
    ? data.skills
        .map(
          (row) =>
            `<a class="skill-row" href="${esc(skillSessionURL(row.name, row.plugin))}"><div><h2>${esc(row.name)}</h2><small>${esc([row.plugin, ...row.scopes.filter(Boolean).slice(0, 16)].filter(Boolean).join(" · "))}</small></div><span class="skill-uses" title="${esc(skillUsesTitle(row))}">${skillUsesLabel(row)}</span><span>${row.sessions.toLocaleString()}</span><span>${row.references.toLocaleString()}</span><time>${esc(date(row.last))}</time></a>`,
        )
        .join("")
    : '<p class="empty">No skills found</p>';
  skillsPager.update({ total: data.total, offset: data.offset, size: 200 });
  const url = new URLSearchParams();
  appendSearchScope(url, { kinds: false });
  if (skillsQuery) url.set("q", skillsQuery);
  if (skillsSort !== "uses") url.set("sort", skillsSort);
  if (skillsOffset) url.set("offset", skillsOffset);
  history.replaceState(null, "", "/skills" + (url.size ? "?" + url : ""));
}
function setupSkills() {
  skillsQuery = new URL(location.href).searchParams.get("q") || "";
  skillsOffset = Number(new URL(location.href).searchParams.get("offset")) || 0;
  $("#skillsearch").value = skillsQuery;
  const sort = new URL(location.href).searchParams.get("sort");
  skillsSort = ["uses", "sessions", "recent"].includes(sort) ? sort : "uses";
  $("#skillsort").value = skillsSort;
  if (skillsMode) $("#skillpage").prepend($("#projectsidebar"));
  $("#skillsort").onchange = () => {
    skillsSort = $("#skillsort").value;
    skillsOffset = 0;
    refresh();
  };
  skillsPager = new Pagination($("#skillpagination"), {
    noun: "skills",
    onChange: (page) => {
      skillsOffset = (page - 1) * 200;
      refresh("top");
    },
  });
  let timer,
    composing = false;
  const search = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      skillsQuery = $("#skillsearch").value;
      skillsOffset = 0;
      refresh();
    }, 250);
  };
  $("#skillsearch").oninput = (event) => {
    if (!composing && !event.isComposing) search();
  };
  $("#skillsearch").oncompositionstart = () => {
    composing = true;
    clearTimeout(timer);
  };
  $("#skillsearch").oncompositionend = () => {
    composing = false;
    search();
  };
  $("#clearskillfilter").onclick = () => {
    skillFilter = "";
    skillPlugin = "";
    sessionOffset = 0;
    refresh();
  };
}
