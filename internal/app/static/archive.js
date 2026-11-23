"use strict";
const archiveSelection = new Map();
let archiveMode = "inactive";
let deletionDays = "";
let deletionOrder = "largest";
let archiveLoading = false;
let archiveOffset = 0,
  archiveData = null,
  archiveStream = null,
  archiveStreamJob = "";
let archiveRequest = null;
const archivePageSize = 200;
let archiveSelectRequest = null;
let archiveCreating = false;
const bytes = (n) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`;
};
const archiveBusy = (job) =>
  job && (job.status === "preparing" || job.status === "compacting" || (job.status === "deleting" && !job.error));
const archiveActive = (job) =>
  job && (archiveBusy(job) || job.status === "ready" || job.status === "deleting");
async function archivePost(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (r.status === 401) {
    $("#archivepanel").close();
    $("#login").hidden = false;
    $("#workspace").hidden = true;
    updateDocumentTitle();
    throw Error("Sign in required.");
  }
  if (!r.ok) throw Error((await r.text()).trim());
  return r.json();
}
function updateArchiveSelection() {
  const n = archiveSelection.size;
  const deleting = archiveMode === "delete";
  const active = archiveActive(archiveData?.job);
  const busy = archiveCreating || archiveLoading || !!archiveSelectRequest;
  const noun = deleting ? "session" : "folder";
  let summary = n ? `${n.toLocaleString()} ${noun}${n === 1 ? "" : "s"} selected` : "";
  if (n && deleting) {
    let selectedBytes = 0;
    for (const session of archiveSelection.values()) selectedBytes += session.raw_bytes;
    summary += ` · ${bytes(selectedBytes)} raw logs`;
    if (n > 2000) summary += " · Select up to 2,000 sessions";
  }
  $("#archiveselected").textContent = archiveSelectRequest ? "Selecting folders…" : summary;
  $("#archivecreate").disabled = !n || busy || !!active || deleting && n > 2000;
  $("#archiveselectall").disabled = busy || !(deleting ? archiveData?.sessions?.length : archiveData?.total_folders);
  $("#archiveclear").hidden = !n;
  $("#archiveclear").disabled = busy;
  $("#deletefilters button").disabled = busy;
  document.querySelectorAll("[data-archive-folder],[data-delete-session]").forEach((el) => {
    el.checked = archiveSelection.has(el.dataset.deleteSession ?? el.dataset.archiveFolder);
    el.disabled = !!active || busy;
  });
}

function deletionRows(sessions) {
  return sessions.map((session) => {
    const date = session.last_activity
      ? `<time datetime="${esc(session.last_activity)}">${esc(new Date(session.last_activity).toLocaleDateString())}</time>`
      : "Activity date unknown";
    return `<label class="archive-row archive-folder-row"><input type="checkbox" data-delete-session="${esc(session.id)}"><span><b>${esc(session.title || session.id)}</b>${session.title ? `<small class="archive-folder-path">${esc(session.id)}</small>` : ""}${session.folder ? `<small class="archive-folder-path">${esc(session.folder)}</small>` : ""}<small>${esc(label(session.source))} · ${date} · ${session.records.toLocaleString()} ${session.records === 1 ? "record" : "records"}</small></span><span>${bytes(session.raw_bytes)}</span></label>`;
  }).join("");
}

async function refreshArchive(signal) {
  const deleting = archiveMode === "delete";
  const path = deleting
    ? "/api/archive/purge?" + new URLSearchParams({ offset: archiveOffset, days: deletionDays, sort: deletionOrder })
    : "/api/archive?group=folders&offset=" + archiveOffset;
  const data = await api(path, signal);
  if (signal?.aborted) return;
  archiveData = data;
  archiveOffset = data.offset;
  const active = !!archiveActive(data.job);
  $("#archivetotal").hidden = deleting && active;
  $("#archivetotal").textContent = deleting
    ? `${data.total_sessions.toLocaleString()} ${data.total_sessions === 1 ? "session" : "sessions"} · ${bytes(data.total_bytes)} raw logs`
    : `${data.total_folders.toLocaleString()} ${data.total_folders === 1 ? "folder" : "folders"} · ${data.total_sessions.toLocaleString()} ${data.total_sessions === 1 ? "session" : "sessions"} · ${bytes(data.total_bytes)}`;
  $("#archivecutoff").innerHTML = deleting ? "All projects" :
    `All projects · Last activity before <time datetime="${esc(data.cutoff)}">${esc(new Date(data.cutoff).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }))}</time>`;
  $("#archivestorage").textContent = data.available_bytes == null
    ? "Storage availability unknown"
    : `Storage available: ${bytes(data.available_bytes)}`;
  $("#deletefilters").hidden = !deleting || active;
  $("#archiveselection").hidden = active;
  $("#archiveitems").hidden = active;
  $("#archiveitems").innerHTML = deleting
    ? deletionRows(data.sessions) || '<p class="empty">No matching sessions</p>'
    : data.folders.length
    ? data.folders
        .map(
          (f) =>
            `<label class="archive-row archive-folder-row"><input type="checkbox" data-archive-folder="${esc(f.path)}" ${archiveActive(data.job) ? "disabled checked" : ""}><span><a href="${esc("/?" + new URLSearchParams({ folder: f.path }))}">${esc(f.path ? folderName(f.path) : "Unassigned")}</a>${f.path ? `<small class="archive-folder-path">${esc(f.path)}</small>` : ""}<small>${f.sessions.toLocaleString()} ${f.sessions === 1 ? "session" : "sessions"} · ${f.records.toLocaleString()} ${f.records === 1 ? "record" : "records"} · <time datetime="${esc(f.last_activity)}">${esc(new Date(f.last_activity).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }))}</time></small></span><span>${bytes(f.raw_bytes)}</span></label>`,
        )
        .join("")
    : '<p class="empty">No inactive folders</p>';
  document.querySelectorAll("[data-archive-folder],[data-delete-session]").forEach((el, i) => {
    const key = el.dataset.deleteSession ?? el.dataset.archiveFolder;
    el.onchange = () => {
      if (el.checked) archiveSelection.set(key, deleting ? data.sessions[i] : null);
      else archiveSelection.delete(key);
      updateArchiveSelection();
    };
  });
  updateArchiveSelection();
  archivePager.update({
    total: deleting ? data.total_sessions : data.total_folders,
    offset: data.offset,
    size: archivePageSize,
  });
  $("#archivepagination").hidden = !!archiveActive(data.job);
  renderArchiveJob(data.job);
  watchArchive(data.job);
}
function renderArchiveJob(job) {
  const key = job ? `${job.id}:${job.status}:${job.error}:${job.compaction_error}` : "";
  if ($("#archivejob").dataset.key === key) {
    const progress = $("#archiveprogress");
    if (progress) progress.textContent = archiveProgress(job);
    return;
  }
  $("#archivejob").dataset.key = key;
  if (!job) {
    $("#archivejob").innerHTML = "";
    return;
  }
  const cancel = ["preparing", "ready"].includes(job.status)
    ? '<button id="archivecancel">Cancel</button>'
    : "";
  let content = "";
  if (job.status === "preparing")
    content = `<h2>Creating ZIP</h2><p id="archiveprogress">${esc(archiveProgress(job))}</p>${cancel}`;
  if (job.status === "ready")
    content = `<h2>Ready to download</h2><p>${job.session_count} sessions · ${job.record_count.toLocaleString()} records · ${bytes(job.archive_bytes)}</p><a id="archivedownload" class="button primary" href="/api/archives/${encodeURIComponent(job.id)}/download" download>Download ZIP</a> ${cancel}<details><summary>SHA-256</summary><code class="archivehash">${esc(job.sha256)}</code></details><form id="archivedeleteform"><label><span>Type <strong>DOWNLOADED</strong> after checking the ZIP</span><input id="archiveconfirmation" autocomplete="off" spellcheck="false" required placeholder="DOWNLOADED"></label><button id="archivedelete" class="danger" disabled>Delete archived data</button></form><p id="archiveactionerror" role="alert"></p>`;
  if (job.status === "deleting")
    content = job.error
      ? `<h2>Deletion paused</h2><p id="archiveprogress">${esc(archiveProgress(job))}</p><p class="failure-detail">${esc(job.error)}</p><button id="archiveretrydelete" class="primary">Retry deletion</button><p id="archiveactionerror" role="alert"></p>`
      : `<h2>${job.direct_delete ? "Deleting records" : "Deleting archived records"}</h2><p id="archiveprogress">${esc(archiveProgress(job))}</p>`;
  if (job.status === "compacting")
    content = "<h2>Reclaiming database space</h2>";
  if (job.status === "deleted")
    content = `<h2>${job.compaction_error ? "Records deleted" : job.direct_delete ? "Deletion complete" : "Archive complete"}</h2><p>${job.deleted_records.toLocaleString()} records deleted · ${bytes(job.reclaimed_bytes)} reclaimed</p>${job.compaction_error ? `<p class="failure-detail">${esc(job.compaction_error)}</p><button id="archivecompact">${job.rebuild_required ? "Rebuild database · pauses ingestion" : "Retry compaction"}</button>` : ""}`;
  if (job.status === "failed")
    content = `<h2 class="failure-detail">${job.direct_delete ? "Deletion failed" : "Archive failed"}</h2><p>${esc(job.error)}</p>${job.deleted_records ? `<p>${job.deleted_records} records already deleted.</p>` : ""}`;
  if (job.status === "canceled") content = "<p>Archive canceled</p>";
  $("#archivejob").innerHTML = content;
  if ($("#archivecancel"))
    $("#archivecancel").onclick = () =>
      archiveAction("/api/archives/" + job.id + "/cancel", {});
  const retryDelete = $("#archiveretrydelete");
  if (retryDelete) {
    retryDelete.onclick = async () => {
      retryDelete.disabled = true;
      try {
        await archiveAction("/api/archives/" + job.id + "/retry-delete", {});
      } finally {
        retryDelete.disabled = false;
      }
    };
  }
  if ($("#archivecompact"))
    $("#archivecompact").onclick = () =>
      archiveAction("/api/archives/" + job.id + "/compact", {
        rebuild: job.rebuild_required,
      });
  if ($("#archiveconfirmation")) {
    $("#archiveconfirmation").oninput = () =>
      ($("#archivedelete").disabled =
        $("#archiveconfirmation").value !== "DOWNLOADED");
    $("#archivedeleteform").onsubmit = async (e) => {
      e.preventDefault();
      $("#archivedelete").disabled = true;
      await archiveAction("/api/archives/" + job.id + "/delete", {
        confirmation: $("#archiveconfirmation").value,
      });
    };
  }
}
function watchArchive(job) {
  if (!$("#archivepanel").open || !archiveBusy(job)) {
    archiveStream?.close();
    archiveStream = null;
    archiveStreamJob = "";
    return;
  }
  if (archiveStream && archiveStreamJob === job.id) return;
  archiveStream?.close();
  const stream = new EventSource(
    "/api/archives/" + encodeURIComponent(job.id) + "/events",
  );
  archiveStream = stream;
  archiveStreamJob = job.id;
  stream.onerror = () => {
    if (archiveStream !== stream) return;
    stream.close();
    archiveStream = null;
    archiveStreamJob = "";
    $("#archiveerror").textContent = "Progress connection lost. Close and reopen this panel.";
  };
  stream.addEventListener("archive", (event) => {
    if (archiveStream !== stream || !$("#archivepanel").open) return;
    const updated = JSON.parse(event.data);
    if (archiveData) archiveData.job = updated;
    renderArchiveJob(updated);
    if (!archiveBusy(updated)) {
      stream.close();
      archiveStream = null;
      archiveStreamJob = "";
      loadArchive().then(() => refresh());
    }
  });
}
function archiveProgress(job) {
  return job.status === "deleting"
    ? `${job.deleted_records.toLocaleString()} / ${job.record_count.toLocaleString()} records`
    : `${job.processed_records.toLocaleString()} / ${job.record_count.toLocaleString()} records · ${bytes(job.archive_bytes)}`;
}
async function archiveAction(path, body) {
  try {
    await archivePost(path, body);
    archiveSelection.clear();
    await loadArchive();
    await refresh();
  } catch (e) {
    const target = $("#archiveactionerror") || $("#archiveerror");
    target.textContent = e.message;
    updateArchiveSelection();
    if ($("#archiveconfirmation")) $("#archiveconfirmation").oninput();
  }
}
async function loadArchive(scrollMode) {
  if (!$("#archivepanel").open) return;
  archiveRequest?.abort();
  const controller = new AbortController();
  archiveRequest = controller;
  archivePager.setLoading(true);
  archiveLoading = true;
  updateArchiveSelection();
  $("#archiveerror").textContent = "";
  try {
    await refreshArchive(controller.signal);
    if (scrollMode === "top") $(".data-panel-body").scrollTop = 0;
  } catch (error) {
    if (error.name !== "AbortError") {
      if (archiveData) archiveOffset = archiveData.offset;
      $("#archiveerror").textContent = error.message;
    }
  } finally {
    if (archiveRequest === controller) {
      archiveLoading = false;
      archivePager.setLoading(false);
      updateArchiveSelection();
    }
  }
}
function openArchive(mode = "inactive") {
  if (mode !== archiveMode) {
    archiveMode = mode;
    watchArchive(null);
    archiveOffset = 0;
    archiveSelection.clear();
    archiveData = null;
    $("#archiveitems").innerHTML = "";
    $("#archivecutoff").textContent = "";
    $("#archivestorage").textContent = "";
    $("#archivetotal").textContent = "";
    renderArchiveJob(null);
  }
  const deleting = mode === "delete";
  $("#archivetitle").textContent = deleting ? "Delete sessions" : "Archive inactive sessions";
  $("#archivecreate").textContent = deleting ? "Delete selected sessions" : "Create ZIP";
  $("#archivecreate").className = deleting ? "danger" : "primary";
  $("#archiveselectall").textContent = deleting ? "Select page" : "Select all";
  archivePager.noun = deleting ? "sessions" : "folders";
  $("#archivepagination").setAttribute("aria-label", deleting ? "Deletion session pages" : "Archive folder pages");
  $("#deletefilters").hidden = !deleting;
  $("#dataoptions").open = false;
  if (!$("#archivepanel").open) $("#archivepanel").showModal();
  listState();
  loadArchive();
}
function setupArchive() {
  $("#openarchive").onclick = () => openArchive();
  $("#opendeletesessions").onclick = () => openArchive("delete");
  $("#deletefilters").onsubmit = (event) => {
    event.preventDefault();
    const days = $("#deleteage").value;
    if (days !== deletionDays) archiveSelection.clear();
    deletionDays = days;
    deletionOrder = $("#deletesort").value;
    archiveOffset = 0;
    loadArchive("top");
  };
  $("#closearchive").onclick = () => $("#archivepanel").close();
  $("#archivepanel").addEventListener("close", () => {
    if ($("#archivepanel").open) return;
    archiveRequest?.abort();
    archiveSelectRequest?.abort();
    archiveSelectRequest = null;
    archiveStream?.close();
    archiveStream = null;
    archiveStreamJob = "";
    archiveRequested = "";
    if (!selected && !skillsMode) listState();
    if ($("#login").hidden) $("#dataoptions summary").focus({ preventScroll: true });
  });
  $("#archiveselectall").onclick = async () => {
    if (archiveMode === "delete") {
      for (const session of archiveData.sessions) archiveSelection.set(session.id, session);
      updateArchiveSelection();
      return;
    }
    const controller = new AbortController();
    const initial = archiveData;
    archiveSelectRequest = controller;
    $("#archiveerror").textContent = "";
    updateArchiveSelection();
    try {
      const paths = new Set();
      for (let offset = 0; offset < initial.total_folders; offset += archivePageSize) {
        const page = offset === initial.offset
          ? initial
          : await api("/api/archive?group=folders&offset=" + offset, controller.signal);
        if (page.offset !== offset || archiveActive(page.job)) {
          throw Error("Archive folders changed. Reopen this panel and try again.");
        }
        page.folders.forEach((folder) => paths.add(folder.path));
      }
      if (!controller.signal.aborted) {
        paths.forEach((path) => archiveSelection.set(path, null));
      }
    } catch (error) {
      if (!controller.signal.aborted) $("#archiveerror").textContent = error.message;
    } finally {
      if (archiveSelectRequest === controller) {
        archiveSelectRequest = null;
        updateArchiveSelection();
        if (document.activeElement === document.body && $("#archivepanel").open) {
          $("#archiveselectall").focus();
        }
      }
    }
  };
  $("#archiveclear").onclick = () => {
    archiveSelection.clear();
    updateArchiveSelection();
    $("#archiveselectall").focus();
  };
  $("#archivecreate").onclick = async () => {
    const direct = archiveMode === "delete";
    const count = archiveSelection.size;
    if (direct && !confirm(`Permanently delete ${count.toLocaleString()} selected ${count === 1 ? "session" : "sessions"}?`)) return;
    archiveCreating = true;
    updateArchiveSelection();
    try {
      if (direct) {
        const sessions = [...archiveSelection.values()].map(({ id, revision }) => ({ id, revision }));
        await archiveAction("/api/archive/purge", { sessions, confirmation: true });
      } else {
        await archiveAction("/api/archives", {
          cutoff: archiveData.cutoff,
          folders: [...archiveSelection.keys()],
        });
      }
    } finally {
      archiveCreating = false;
      updateArchiveSelection();
    }
  };
  addEventListener("pagehide", () => archiveStream?.close());
}
