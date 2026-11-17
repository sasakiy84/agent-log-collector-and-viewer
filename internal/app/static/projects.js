"use strict";
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
const projectNavigation = (() => {
  let projects = [],
    projectsKey = "",
    projectsQuery = "",
    projectsTotal = 0,
    projectsRequest = null,
    projectLoadSerial = 0;
  function renderProjects() {
    const selection = selectedProjects();
    $("#projectexport").hidden = skillsMode || !selection.length;
    $("#skillsnav").href =
      "/skills" +
      (() => { const params = appendSearchScope(new URLSearchParams(), { kinds: false, text: false }); return params.size ? "?" + params : ""; })();
    const sessionParams = appendSearchScope(new URLSearchParams(), { kinds: false, text: false });
    $("#sessionsnav").href = "/" + (sessionParams.size ? "?" + sessionParams : "");
    const focused = document.activeElement?.dataset.project;
    const focusedCheck = document.activeElement?.dataset.projectCheck;
    const all = $("#allprojects");
    all.classList.toggle("active", !selection.length);
    all.setAttribute("aria-current", !selection.length ? "true" : "false");
    const rows = [...projects];
    for (const id of selection) if (!rows.some((p) => p.id === id)) rows.unshift({ id, name: projectName(id), count: 0 });
    $("#projectlist").innerHTML = rows
      .map(
        (p) =>
          `<div class="project-option"><input type="checkbox" data-project-check="${esc(p.id)}" aria-label="Include project ${esc(p.name)}" ${selection.includes(p.id) ? "checked" : ""}><button class="project-choice ${selection.includes(p.id) ? "active" : ""}" data-project="${esc(p.id)}" aria-current="${selection.includes(p.id) ? "true" : "false"}" title="${esc(p.name)}"><span>${esc(p.name)}</span>${p.count !== undefined ? `<small>${p.count.toLocaleString()}</small>` : ""}</button></div>`,
      )
      .join("");
    $("#projectlist")
      .querySelectorAll("[data-project]")
      .forEach(
        (button) =>
          (button.onclick = () => chooseProject(button.dataset.project)),
      );
    $("#projectlist").querySelectorAll("[data-project-check]").forEach((input) => { input.onchange = () => projectCheckbox(input.dataset.projectCheck, input.checked); });
    if (focusedCheck !== undefined) $("#projectlist").querySelector(`[data-project-check="${CSS.escape(focusedCheck)}"]`)?.focus({ preventScroll: true });
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
    projectSelections = [];
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
  async function loadProjects(parentSignal, more = false) {
    projectsRequest?.abort();
    projectsRequest = new AbortController();
    const signal = parentSignal ? AbortSignal.any([parentSignal, projectsRequest.signal]) : projectsRequest.signal;
    const serial = ++projectLoadSerial;
    const params = appendSearchScope(new URLSearchParams(), { projects: false, kinds: !skillsMode });
    if (skillsMode) {params.delete("exclude");params.delete("text_mode");}
    if (!skillsMode) {
      if (query) params.set("session_q", query);
      if (idValue) {params.set("id_value", idValue); if(idKey) params.set("id_key", idKey);}
      if (skillFilter) {params.set("skill", skillFilter);params.set("plugin", skillPlugin);}
    }
    params.set("q", projectsQuery);
    if (includeInternalSessions) params.set("diagnostics", "1");
    const key = params.toString();
    if (!more && projectsKey === key) {
      renderProjects();
      return;
    }
    params.set("offset", String(more ? projects.length : 0));
    let data;
    try {
      data = await api("/api/projects?" + params, signal);
    } catch (error) {
      // Replacing only the sidebar search must not fail a parallel list load.
      if (signal.aborted && !parentSignal?.aborted) return;
      throw error;
    }
    if (signal?.aborted || serial !== projectLoadSerial) return;
    projects = more && projectsKey === key ? [...projects, ...data.projects] : data.projects;
    projectsKey = key;
    projectsTotal = data.total;
    renderProjects();
  }
  function setupProjects() {
    $("#projectexport").onclick = () => {
      const params = new URLSearchParams({ diagnostics: "1" });
      for (const id of selectedProjects()) params.append("project", id);
      $("#dataoptions").open = false;
      location.assign("/api/export/download?" + params);
    };
    $("#sessionsnav").setAttribute("aria-current", skillsMode || settingsMode ? "false" : "page");
    $("#skillsnav").setAttribute("aria-current", skillsMode ? "page" : "false");
    $("#settingsnav").setAttribute("aria-current", settingsMode ? "page" : "false");
    $("#allprojects").onclick = () => chooseProject(null);
    let timer,
      composing = false;
    const search = () => {
      clearTimeout(timer);
      timer = setTimeout(async () => {
        projectsQuery = $("#projectsearch").value;
        try {
          await loadProjects();
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
      $("#moreprojects").disabled = true;
      try {
        await loadProjects(undefined, true);
      } catch (e) {
        if (e.name !== "AbortError") $("#projectstatus").textContent = e.message;
      } finally {
        $("#moreprojects").disabled = false;
      }
    };
  }

  return { render: renderProjects, load: loadProjects, setup: setupProjects };
})();
