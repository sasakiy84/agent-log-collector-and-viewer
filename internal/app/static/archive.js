"use strict";
const archiveSelection = new Set();
let archiveOffset = 0,
  archiveData = null,
  archiveStream = null,
  archiveStreamJob = "";
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
  job && ["preparing", "deleting", "compacting"].includes(job.status);
const archiveActive = (job) =>
  job && (archiveBusy(job) || job.status === "ready");
async function archivePost(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (r.status === 401) {
    $("#login").hidden = false;
    $("#workspace").hidden = true;
    throw Error("Sign in required.");
  }
  if (!r.ok) throw Error((await r.text()).trim());
  return r.json();
}
function updateArchiveSelection() {
  const n = archiveSelection.size;
  $("#archiveselected").textContent = n
    ? `${n} ${n === 1 ? "folder" : "folders"} selected`
    : "";
  $("#archivecreate").disabled = !n;
  $("#archiveclear").hidden = !n;
  document
    .querySelectorAll("[data-archive-folder]")
    .forEach(
      (el) =>
        (el.checked =
          archiveActive(archiveData?.job) ||
          archiveSelection.has(el.dataset.archiveFolder)),
    );
}
async function refreshArchive(signal) {
  const data = await api(
    "/api/archive?group=folders&offset=" + archiveOffset,
    signal,
  );
  if (signal?.aborted) return;
  archiveData = data;
  archiveOffset = data.offset;
  $("#archivetotal").textContent =
    `${data.total_folders.toLocaleString()} folders · ${data.total_sessions.toLocaleString()} sessions · ${bytes(data.total_bytes)}`;
  $("#archivecutoff").textContent =
    `Last activity before ${new Date(data.cutoff).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })}`;
  $("#archiveselection").hidden = !!archiveActive(data.job);
  $("#archiveitems").innerHTML = data.folders.length
    ? data.folders
        .map(
          (f) =>
            `<label class="archive-row archive-folder-row"><input type="checkbox" data-archive-folder="${esc(f.path)}" ${archiveActive(data.job) ? "disabled checked" : ""}><span><a href="${esc("/?" + new URLSearchParams({ folder: f.path }))}">${esc(f.path ? folderName(f.path) : "Unassigned")}</a>${f.path ? `<small class="archive-folder-path">${esc(f.path)}</small>` : ""}<small>${f.sessions.toLocaleString()} ${f.sessions === 1 ? "session" : "sessions"} · ${f.records.toLocaleString()} ${f.records === 1 ? "record" : "records"} · ${esc(new Date(f.last_activity).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }))}</small></span><span>${bytes(f.raw_bytes)}</span></label>`,
        )
        .join("")
    : '<p class="empty">No inactive folders</p>';
  document.querySelectorAll("[data-archive-folder]").forEach(
    (el) =>
      (el.onchange = () => {
        if (el.checked) archiveSelection.add(el.dataset.archiveFolder);
        else archiveSelection.delete(el.dataset.archiveFolder);
        updateArchiveSelection();
      }),
  );
  updateArchiveSelection();
  archivePager.update({
    total: data.total_folders,
    offset: data.offset,
    size: 200,
  });
  renderArchiveJob(data.job);
  watchArchive(data.job);
}
function renderArchiveJob(job) {
  const key = job ? `${job.id}:${job.status}:${job.compaction_error}` : "";
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
    content = `<h2>Ready to download</h2><p>${job.session_count} sessions · ${job.record_count.toLocaleString()} records · ${bytes(job.archive_bytes)}</p><a id="archivedownload" class="button primary" href="/api/archives/${encodeURIComponent(job.id)}/download" download>Download ZIP</a> ${cancel}<details><summary>SHA-256</summary><code class="archivehash">${esc(job.sha256)}</code></details><form id="archivedeleteform"><label>Download confirmed: type <strong>DOWNLOADED</strong><input id="archiveconfirmation" autocomplete="off" spellcheck="false" required placeholder="DOWNLOADED"></label><button id="archivedelete" class="danger" disabled>Delete archived data</button></form><p id="archiveactionerror" role="alert"></p>`;
  if (job.status === "deleting")
    content = `<h2>Deleting archived records</h2><p id="archiveprogress">${esc(archiveProgress(job))}</p>`;
  if (job.status === "compacting")
    content = "<h2>Reclaiming database space</h2>";
  if (job.status === "deleted")
    content = `<h2>Archive complete</h2><p>${job.deleted_records.toLocaleString()} records deleted · ${bytes(job.reclaimed_bytes)} reclaimed</p>${job.compaction_error ? `<p class="failure-detail">${esc(job.compaction_error)}</p><button id="archivecompact">${job.rebuild_required ? "Rebuild database · pauses ingestion" : "Retry compaction"}</button>` : ""}`;
  if (job.status === "failed")
    content = `<h2 class="failure-detail">Archive failed</h2><p>${esc(job.error)}</p>${job.deleted_records ? `<p>${job.deleted_records} records already deleted.</p>` : ""}`;
  if (job.status === "canceled") content = "<p>Archive canceled</p>";
  $("#archivejob").innerHTML = content;
  if ($("#archivecancel"))
    $("#archivecancel").onclick = () =>
      archiveAction("/api/archives/" + job.id + "/cancel", {});
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
  if (!archiveBusy(job)) {
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
    $("#error").textContent = "Progress connection lost. Reload the page.";
  };
  stream.addEventListener("archive", (event) => {
    if (archiveStream !== stream) return;
    const updated = JSON.parse(event.data);
    if (archiveData) archiveData.job = updated;
    renderArchiveJob(updated);
    if (!archiveBusy(updated)) {
      stream.close();
      archiveStream = null;
      archiveStreamJob = "";
      if (updated.status !== "ready") refresh();
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
    await refresh();
  } catch (e) {
    const target = $("#archiveactionerror") || $("#error");
    target.textContent = e.message;
    updateArchiveSelection();
    if ($("#archiveconfirmation")) $("#archiveconfirmation").oninput();
  }
}
function setupArchive() {
  $("#archiveclear").onclick = () => {
    archiveSelection.clear();
    updateArchiveSelection();
  };
  $("#archivecreate").onclick = async () => {
    $("#archivecreate").disabled = true;
    await archiveAction("/api/archives", {
      cutoff: archiveData.cutoff,
      folders: [...archiveSelection],
    });
  };
  addEventListener("pagehide", () => archiveStream?.close());
}
