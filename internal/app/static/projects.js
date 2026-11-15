"use strict";
let projects = [],
  projectsKey = "",
  projectsQuery = "",
  projectsTotal = 0,
  projectsRequest = null;
function canonicalProjectID(id) {
  if (!id.startsWith("path:")) return id;
  const directory = id.slice(5);
  return directory ? "name:" + folderName(directory) : "";
}
function projectName(id) {
  id = canonicalProjectID(id);
  if (id === "") return "Unassigned";
  if (id.startsWith("name:")) return id.slice(5);
  return id;
}
function renderProjects() {
  $("#projectexport").hidden = exportMode || project === null;
  $("#projectexport").href = "/export?" + new URLSearchParams({ project: project ?? "", diagnostics: "1" });
  $("#skillsnav").href =
    "/skills" +
    (project !== null ? "?" + new URLSearchParams({ project }) : "");
  const focused = document.activeElement?.dataset.project;
  const all = $("#allprojects");
  all.classList.toggle("active", project === null);
  all.setAttribute("aria-current", project === null ? "true" : "false");
  const rows = [...projects];
  if (project !== null && !rows.some((p) => p.id === project))
    rows.unshift({
      id: project,
      name: projectName(project),
    });
  $("#projectlist").innerHTML = rows
    .map(
      (p) =>
        `<button class="project-choice ${p.id === project ? "active" : ""}" data-project="${esc(p.id)}" aria-current="${p.id === project ? "true" : "false"}" title="${esc(p.name)}"><span>${esc(p.name)}</span>${p.count !== undefined ? `<small>${p.count.toLocaleString()}</small>` : ""}</button>`,
    )
    .join("");
  $("#projectlist")
    .querySelectorAll("[data-project]")
    .forEach(
      (button) =>
        (button.onclick = () => chooseProject(button.dataset.project)),
    );
  if (focused !== undefined)
    $("#projectlist")
      .querySelector(`[data-project="${CSS.escape(focused)}"]`)
      ?.focus({ preventScroll: true });
  $("#moreprojects").hidden = projects.length >= projectsTotal;
  $("#projectstatus").textContent =
    !projects.length && projectsKey ? "No projects" : "";
}
function chooseProject(id) {
  project = id;
  renderProjects();
  if (skillsMode) {
    skillsOffset = 0;
  } else {
    folder = null;
    sessionOffset = 0;
    listState();
  }
  refresh();
}
async function loadProjects(signal, more = false) {
  const key = JSON.stringify([$("#showdiagnostics").checked, projectsQuery, exportMode]);
  if (!more && projectsKey === key) {
    renderProjects();
    return;
  }
  const params = new URLSearchParams({
    q: projectsQuery,
    offset: String(more ? projects.length : 0),
  });
  if ($("#showdiagnostics").checked) params.set("diagnostics", "1");
  if (exportMode) params.set("include_children", "1");
  const data = await api("/api/projects?" + params, signal);
  if (
    signal?.aborted ||
    key !== JSON.stringify([$("#showdiagnostics").checked, projectsQuery, exportMode])
  )
    return;
  projects = more ? [...projects, ...data.projects] : data.projects;
  projectsKey = key;
  projectsTotal = data.total;
  renderProjects();
}
function setupProjects() {
  $("#allprojects").onclick = () => chooseProject(null);
  let timer,
    composing = false;
  const search = () => {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      projectsQuery = $("#projectsearch").value;
      projectsRequest?.abort();
      const controller = new AbortController();
      projectsRequest = controller;
      try {
        await loadProjects(controller.signal);
      } catch (e) {
        if (e.name !== "AbortError")
          $("#projectstatus").textContent = e.message;
      }
    }, 200);
  };
  $("#projectsearch").addEventListener("compositionstart", () => {
    composing = true;
    clearTimeout(timer);
  });
  $("#projectsearch").addEventListener("compositionend", () => {
    composing = false;
    search();
  });
  $("#projectsearch").oninput = (e) => {
    if (!composing && !e.isComposing) search();
  };
  $("#moreprojects").onclick = async () => {
    projectsRequest?.abort();
    const controller = new AbortController();
    projectsRequest = controller;
    $("#moreprojects").disabled = true;
    try {
      await loadProjects(controller.signal, true);
    } catch (e) {
      if (e.name !== "AbortError") $("#projectstatus").textContent = e.message;
    } finally {
      $("#moreprojects").disabled = false;
    }
  };
}
