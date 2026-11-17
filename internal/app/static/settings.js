"use strict";
let settingsKeys = [];
let ingestionRequest = 0, ingestionLoading = false;
async function refreshIngestion(signal) {
  const serial = ++ingestionRequest;
  ingestionLoading = true;
  $("#refreshingestion").disabled = true;
  try {
    const metrics = await api("/api/metrics", signal);
    if (signal?.aborted || serial !== ingestionRequest) return;
    $("#serverversion").textContent = `Version ${metrics.version}`;
    $("#pendingrecords").textContent = Number.isSafeInteger(metrics.pending_records) ? metrics.pending_records.toLocaleString() : "—";
    $("#pendingbatches").textContent = Number.isSafeInteger(metrics.pending_batches) ? metrics.pending_batches.toLocaleString() : "—";
    $("#pendingbytes").textContent = Number.isSafeInteger(metrics.pending_bytes) ? bytes(metrics.pending_bytes) : "—";
    $("#ingestionerror").textContent = "";
  } catch (error) {
    if (signal?.aborted || serial !== ingestionRequest) return;
    for (const id of ["pendingrecords", "pendingbatches", "pendingbytes"]) $("#" + id).textContent = "—";
    $("#ingestionerror").textContent = `Unable to load ingestion status: ${error.message}`;
  } finally {
    if (serial === ingestionRequest) {
      ingestionLoading = false;
      $("#refreshingestion").disabled = false;
    }
  }
}
function renderAPIKeys() {
  $("#apikeycount").textContent = `${settingsKeys.length} ${settingsKeys.length === 1 ? "key" : "keys"}`;
  $("#apikeyrows").innerHTML = settingsKeys.length ? settingsKeys.map(key => `
    <article class="api-key-row" data-key-id="${esc(key.id)}">
      <div><form class="api-key-name"><label class="visually-hidden" for="name-${esc(key.id)}">Name</label><input id="name-${esc(key.id)}" name="name" value="${esc(key.name)}" maxlength="120" required aria-label="Name for ${esc(key.name)}"><button type="submit" hidden>Save</button></form><small class="muted">Created ${esc(date(key.created_at))}</small></div>
      <div class="api-key-secret"><input type="password" readonly value="${esc(key.key)}" aria-label="API key for ${esc(key.name)}" autocomplete="off" spellcheck="false"><button type="button" data-action="reveal" aria-pressed="false">Show</button><button type="button" data-action="copy">Copy</button></div>
      <button type="button" data-action="delete" aria-label="Delete ${esc(key.name)}">Delete</button>
    </article>`).join("") : '<p class="empty">No API keys</p>';
  const byID = new Map(settingsKeys.map(key => [key.id, key]));
  $("#apikeyrows").querySelectorAll("[data-key-id]").forEach(row => {
    const key = byID.get(row.dataset.keyId), form = row.querySelector("form"), name = form.elements.name;
    name.oninput = () => { form.querySelector("button").hidden = name.value.trim() === key.name; };
    form.onsubmit = async event => {
      event.preventDefault();
      await settingsAction(async () => {
        await settingsMutation(`/api/settings/api-keys/${encodeURIComponent(key.id)}`, "PATCH", {name: name.value});
        await refreshSettings();
        $("#settingsstatus").textContent = "Name saved";
      });
    };
    row.querySelector('[data-action="reveal"]').onclick = event => {
      const input = row.querySelector(".api-key-secret input"), reveal = input.type === "password";
      input.type = reveal ? "text" : "password";
      event.currentTarget.textContent = reveal ? "Hide" : "Show";
      event.currentTarget.setAttribute("aria-pressed", String(reveal));
    };
    row.querySelector('[data-action="copy"]').onclick = () => settingsAction(async () => {
      await navigator.clipboard.writeText(key.key);
      $("#settingsstatus").textContent = "Key copied";
    });
    row.querySelector('[data-action="delete"]').onclick = () => settingsAction(async () => {
      if (!confirm(`Delete API key “${key.name}”?`)) return;
      await settingsMutation(`/api/settings/api-keys/${encodeURIComponent(key.id)}`, "DELETE");
      await refreshSettings();
      $("#settingsstatus").textContent = "Key deleted";
    });
  });
}
async function settingsMutation(path, method, body) {
  return apiResponse(path, undefined, {
    method,
    headers: {"Content-Type": "application/json"},
    ...(body ? {body: JSON.stringify(body)} : {}),
  });
}
async function settingsAction(action) {
  $("#settingserror").textContent = "";
  $("#settingsstatus").textContent = "";
  $("#settingspage").querySelectorAll("button").forEach(button => { button.disabled = true; });
  try { await action(); }
  catch (error) { $("#settingserror").textContent = error.message; }
  finally { $("#settingspage").querySelectorAll("button").forEach(button => { button.disabled = false; }); }
}
async function refreshSettings(signal) {
  const [data] = await Promise.all([api("/api/settings/api-keys", signal), refreshIngestion(signal)]);
  if (signal?.aborted) return;
  settingsKeys = data.keys;
  renderAPIKeys();
}
function setupSettings() {
  if (!settingsMode) return;
  $("#refreshingestion").onclick = () => refreshIngestion();
  setInterval(() => {
    if (!document.hidden && !$("#workspace").hidden && !ingestionLoading) refreshIngestion();
  }, 5000);
  $("#apireadmecurl").textContent = `curl -fsS -H 'Authorization: Bearer <API_KEY>' '${location.origin}/api/readme'`;
  $("#copyreadmecurl").onclick = () => settingsAction(async () => {
    await navigator.clipboard.writeText($("#apireadmecurl").textContent);
    $("#settingsstatus").textContent = "Command copied";
  });
  $("#createapikey").onsubmit = async event => {
    event.preventDefault();
    const name = $("#apikeyname").value;
    await settingsAction(async () => {
      await settingsMutation("/api/settings/api-keys", "POST", {name});
      $("#apikeyname").value = "";
      await refreshSettings();
      $("#settingsstatus").textContent = "Key created";
    });
  };
}
