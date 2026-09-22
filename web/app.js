/* Jellyfin Anime Organizer — frontend */
"use strict";

const $ = (id) => document.getElementById(id);

const state = {
  users: [],
  userId: "",
  viewId: "",
  series: [],
  show: null, // {id, name}
  episodes: [],
  rules: [],
  pollTimer: null,
  currentRunId: null,
};

// ---------- API ----------

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

// ---------- health ----------

async function loadHealth() {
  const el = $("health");
  try {
    const h = await api("/api/health");
    if (!h.configured) {
      el.textContent = "not configured — add your Jellyfin URL and API key in ⚙ Settings";
      el.className = "health bad";
      openSettings();
    } else if (h.connected) {
      el.textContent = `${h.serverName} · Jellyfin ${h.version}`;
      el.className = "health ok";
    } else {
      el.textContent = `cannot reach ${h.jellyfin}: ${h.error || "unknown error"}`;
      el.className = "health bad";
    }
    $("schedInfo").textContent = h.scheduleMinutes > 0
      ? `schedule (every ${formatMinutes(h.scheduleMinutes)})`
      : "schedule (off)";
  } catch (e) {
    el.textContent = e.message;
    el.className = "health bad";
  }
}

function formatMinutes(m) {
  return m % 60 === 0 ? `${m / 60}h` : `${m}m`;
}

// ---------- settings ----------

let settingsLoaded = false;

async function openSettings() {
  const p = $("settingsPanel");
  if (!p.hidden) return;
  p.hidden = false;
  if (!settingsLoaded) await loadSettings().catch(alertErr);
}

async function loadSettings() {
  const c = await api("/api/config");
  $("cfgPath").textContent = c.configPath;
  $("cfgUrl").value = c.jellyfinUrl || "";
  $("cfgUser").value = c.username || "";
  $("cfgDelay").value = c.webhookDelaySeconds;
  $("cfgSched").value = c.scheduleIntervalMinutes;
  $("cfgPort").value = c.port;
  $("cfgKey").value = "";
  $("cfgKey").placeholder = c.apiKeySet ? "•••••• saved — leave blank to keep" : "paste your Jellyfin API key";
  $("cfgToken").value = "";
  $("cfgToken").placeholder = c.webhookTokenSet ? "•••••• saved — leave blank to keep" : "leave empty to disable";
  settingsLoaded = true;
}

async function saveSettings() {
  const body = {
    jellyfinUrl: $("cfgUrl").value.trim(),
    username: $("cfgUser").value.trim(),
    webhookDelaySeconds: Math.max(0, Number($("cfgDelay").value) || 0),
    scheduleIntervalMinutes: Math.max(0, Number($("cfgSched").value) || 0),
    port: String($("cfgPort").value).trim(),
  };
  // secrets are only sent when the user typed a new value
  if ($("cfgKey").value.trim()) body.apiKey = $("cfgKey").value.trim();
  if ($("cfgToken").value.trim()) body.webhookToken = $("cfgToken").value.trim();
  const st = $("cfgStatus");
  st.textContent = "saving…";
  try {
    await api("/api/config", { method: "POST", body });
    st.textContent = "saved ✓";
    settingsLoaded = false;
    await loadSettings();
    await loadHealth();
    loadUsers();
  } catch (e) {
    st.textContent = "";
    alert("Save failed: " + e.message);
  }
}

// ---------- browse ----------

// Remember the last user/library picked so a page reload doesn't make you
// re-navigate from scratch — pure per-browser convenience, nothing server-side.
const LAST_PICK_KEY = "jfo.lastPick";
function saveLastPick() {
  try { localStorage.setItem(LAST_PICK_KEY, JSON.stringify({ userId: state.userId, viewId: state.viewId })); } catch {}
}
function loadLastPick() {
  try { return JSON.parse(localStorage.getItem(LAST_PICK_KEY) || "{}"); } catch { return {}; }
}

async function loadUsers() {
  const sel = $("userSelect");
  try {
    state.users = await api("/api/users");
  } catch (e) {
    sel.innerHTML = '<option value="">— failed to load users —</option>';
    return;
  }
  sel.innerHTML = '<option value="">— pick a user —</option>' +
    state.users.map((u) => `<option value="${u.id}">${esc(u.name)}</option>`).join("");
  const last = loadLastPick();
  if (last.userId && state.users.some((u) => u.id === last.userId)) {
    sel.value = last.userId;
    await onUserChange(last.viewId);
  }
}

async function onUserChange(restoreViewId) {
  state.userId = $("userSelect").value;
  state.viewId = "";
  state.series = [];
  saveLastPick();
  renderSeriesList();
  const sel = $("viewSelect");
  sel.disabled = !state.userId;
  $("seriesSearch").disabled = true;
  sel.innerHTML = "<option value=''>—</option>";
  $("seriesList").innerHTML = '<li class="muted">loading libraries…</li>';
  if (!state.userId) {
    renderSeriesList();
    return;
  }
  const views = await api(`/api/users/${state.userId}/views`);
  sel.innerHTML = '<option value="">— pick a library —</option>' +
    views.map((v) => `<option value="${v.id}">${esc(v.name)} [${esc(v.collectionType)}]</option>`).join("");
  renderSeriesList();
  if (restoreViewId && views.some((v) => v.id === restoreViewId)) {
    sel.value = restoreViewId;
    await onViewChange();
  }
}

async function onViewChange() {
  state.viewId = $("viewSelect").value;
  state.series = [];
  saveLastPick();
  $("seriesSearch").disabled = !state.viewId;
  if (!state.viewId) {
    renderSeriesList();
    return;
  }
  $("seriesList").innerHTML = '<li class="muted">loading shows…</li>';
  state.series = await api(`/api/users/${state.userId}/views/${state.viewId}/series`);
  renderSeriesList();
}

function displayName(s) {
  // avoid "Bleach (2004) (2004)" when the name already carries the year
  return s.year && !s.name.includes(`(${s.year})`) ? `${s.name} (${s.year})` : s.name;
}

function renderSeriesList() {
  const q = $("seriesSearch").value.trim().toLowerCase();
  const list = $("seriesList");
  list.innerHTML = "";
  if (!state.userId) {
    list.innerHTML = '<li class="muted">pick a user and library</li>';
    return;
  }
  if (!state.viewId) {
    list.innerHTML = '<li class="muted">pick a library</li>';
    return;
  }
  let shown = 0;
  for (const s of state.series) {
    if (q && !s.name.toLowerCase().includes(q)) continue;
    const li = document.createElement("li");
    li.textContent = displayName(s);
    li.className = state.show && state.show.id === s.id ? "active" : "";
    li.onclick = () => selectShow(s);
    list.appendChild(li);
    shown++;
  }
  if (shown === 0) {
    list.innerHTML = `<li class="muted">${state.series.length ? "no matches" : "no shows in this library"}</li>`;
  }
}

async function selectShow(s) {
  state.show = s;
  renderSeriesList();
  $("showEmpty").hidden = true;
  $("showContent").hidden = false;
  $("showTitle").textContent = displayName(s);
  $("epStats").textContent = "loading episodes…";
  // Reset per-show fields before loading a rule (if any) — otherwise the
  // previous show's filler/skip ranges and action leak into this one.
  const rule = state.rules.find((r) => r.seriesId === s.id);
  $("fillerRanges").value = rule?.fillerRanges || "";
  $("skipRanges").value = rule?.skipRanges || "";
  $("actionSelect").value = rule?.action || "set_absolute";
  $("nfoRefresh").checked = rule ? rule.nfoRefresh : true;
  $("autoRule").checked = rule ? rule.auto : true;

  state.episodes = await api(`/api/series/${s.id}/episodes`);
  renderEpisodes();
}

// ---------- range editing (filler/skip fields <-> click-to-toggle table chips) ----------

// Parses "26, 97, 101-106" into a Set of numbers. Silently ignores junk
// fragments — this only drives client-side chip highlighting, the server
// re-validates for real when a run/rule is submitted.
function parseRangesClient(s) {
  const set = new Set();
  for (const part of (s || "").split(",")) {
    const p = part.trim();
    if (!p) continue;
    if (p.includes("-")) {
      const [a, b] = p.split("-").map((x) => parseInt(x.trim(), 10));
      if (Number.isFinite(a) && Number.isFinite(b)) {
        for (let n = Math.min(a, b); n <= Math.max(a, b); n++) set.add(n);
      }
    } else {
      const n = parseInt(p, 10);
      if (Number.isFinite(n)) set.add(n);
    }
  }
  return set;
}

// Collapses a Set of numbers back into "26, 97, 101-106" form.
function formatRanges(set) {
  const nums = [...set].sort((a, b) => a - b);
  const parts = [];
  let start = null, prev = null;
  for (const n of nums) {
    if (start === null) { start = prev = n; continue; }
    if (n === prev + 1) { prev = n; continue; }
    parts.push(start === prev ? `${start}` : `${start}-${prev}`);
    start = prev = n;
  }
  if (start !== null) parts.push(start === prev ? `${start}` : `${start}-${prev}`);
  return parts.join(", ");
}

function toggleRangeNumber(input, num) {
  const set = parseRangesClient(input.value);
  if (set.has(num)) set.delete(num); else set.add(num);
  input.value = formatRanges(set);
  renderEpisodes();
}

function chipToggle(label, title, on, kind, onclick) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = `chip-toggle ${kind}${on ? " on" : ""}`;
  b.textContent = label;
  b.title = title;
  b.onclick = (e) => { e.stopPropagation(); onclick(); };
  return b;
}

function renderEpisodes() {
  const eps = state.episodes;
  const mismatches = eps.filter((e) => e.indexMismatch).length;
  const fillers = eps.filter((e) => e.isFiller).length;
  $("epStats").textContent =
    `${eps.length} episodes · ${mismatches} index mismatch(es) · ${fillers} tagged filler`;

  const fillerInput = $("fillerRanges");
  const skipInput = $("skipRanges");
  const fillerSet = parseRangesClient(fillerInput.value);
  const skipSet = parseRangesClient(skipInput.value);

  const tbody = $("epTable").querySelector("tbody");
  tbody.innerHTML = "";
  for (const ep of eps) {
    const tr = document.createElement("tr");
    const classes = [];
    if (ep.indexMismatch) classes.push("mismatch");
    if (ep.absolute > 0 && skipSet.has(ep.absolute)) classes.push("skip-staged");
    tr.className = classes.join(" ");
    const se = `S${pad(ep.season)}E${pad(ep.episode)}`;
    tr.innerHTML =
      `<td class="num">${se}</td>` +
      `<td class="num idx">${ep.episode ?? "—"}</td>` +
      `<td class="num abs">${ep.absolute || "—"}</td>` +
      `<td class="num tags"></td>` +
      `<td class="title"></td>` +
      `<td class="file" title="${esc(ep.fileName)}">${esc(ep.fileName)}</td>`;

    if (ep.absolute > 0) {
      const tagsTd = tr.querySelector(".tags");
      tagsTd.append(
        chipToggle("F", `Toggle abs ${ep.absolute} in the filler field`, fillerSet.has(ep.absolute), "filler",
          () => toggleRangeNumber(fillerInput, ep.absolute)),
        chipToggle("S", `Toggle abs ${ep.absolute} in the skip field`, skipSet.has(ep.absolute), "skip",
          () => toggleRangeNumber(skipInput, ep.absolute)),
      );
    }

    const titleTd = tr.querySelector(".title");
    titleTd.append(ep.title || "(untitled)");
    if (ep.isFiller) {
      const b = document.createElement("span");
      b.className = "badge filler";
      b.textContent = "FILLER";
      titleTd.append(" ", b);
    }
    titleTd.onclick = () => editTitle(ep, titleTd);
    tbody.appendChild(tr);
  }
}

function editTitle(ep, td) {
  if (td.querySelector("input")) return;
  const input = document.createElement("input");
  input.type = "text";
  input.value = ep.title;
  td.replaceChildren(input);
  input.focus();
  input.select();
  const done = () => { renderEpisodes(); };
  input.onkeydown = async (e) => {
    if (e.key === "Escape") return done();
    if (e.key !== "Enter") return;
    const title = input.value.trim();
    if (!title || title === ep.title) return done();
    input.disabled = true;
    try {
      await api(`/api/items/${ep.id}/title`, { method: "POST", body: { userId: state.userId, title } });
      ep.title = title;
      ep.isFiller = title.includes("(FILLER)");
    } catch (err) {
      alert("Rename failed: " + err.message);
    }
    done();
  };
  input.onblur = done;
}

// ---------- actions / runs ----------

function currentJob() {
  return {
    userId: state.userId,
    seriesId: state.show.id,
    seriesName: state.show.name,
    action: $("actionSelect").value,
    fillerRanges: $("fillerRanges").value.trim(),
    skipRanges: $("skipRanges").value.trim(),
  };
}

function needsRanges(action) {
  return action === "tag_filler" || action === "both";
}

async function runCurrent(dryRun) {
  const job = currentJob();
  if (needsRanges(job.action) && !job.fillerRanges) {
    alert("Enter the filler episode numbers first (e.g. 26, 97, 101-106).");
    return;
  }
  if (!dryRun && !confirm(`Apply “${$("actionSelect").selectedOptions[0].text}” to ${state.show.name}?`)) return;
  await startRun({
    dryRun,
    nfoRefresh: $("nfoRefresh").checked,
    label: state.show.name,
    jobs: [job],
  });
}

async function startRun(payload) {
  try {
    const { runId } = await api("/api/runs", { method: "POST", body: payload });
    openLog(runId);
    loadRuns();
  } catch (e) {
    alert("Run failed to start: " + e.message);
  }
}

function openLog(runId) {
  state.currentRunId = runId;
  $("logDrawer").hidden = false;
  $("logBody").textContent = "";
  pollRun();
}

async function pollRun() {
  clearTimeout(state.pollTimer);
  if (!state.currentRunId) return;
  let run;
  try {
    run = await api(`/api/runs/${state.currentRunId}`);
  } catch {
    return;
  }
  $("logTitle").textContent = `${run.dryRun ? "Preview" : "Run"}: ${run.label}`;
  const st = $("logStatus");
  st.textContent = run.status + (run.dryRun ? " · dry-run" : "") +
    ` · ${run.changed} change(s)` + (run.errors ? ` · ${run.errors} error(s)` : "");
  st.className = "badge " + run.status;

  const body = $("logBody");
  const atBottom = body.scrollTop + body.clientHeight >= body.scrollHeight - 8;
  body.innerHTML = (run.entries || [])
    .map((e) => `<span class="${e.level}">${esc(e.message)}</span>`)
    .join("\n");
  if (atBottom) body.scrollTop = body.scrollHeight;

  if (run.status === "running") {
    state.pollTimer = setTimeout(pollRun, 1000);
  } else {
    loadRuns();
    // refresh the table after a real (non-dry) run on the open show
    if (!run.dryRun && state.show) {
      state.episodes = await api(`/api/series/${state.show.id}/episodes`);
      renderEpisodes();
    }
  }
}

// ---------- rules ----------

async function loadRules() {
  state.rules = await api("/api/rules");
  const list = $("rulesList");
  list.innerHTML = "";
  if (!state.rules.length) {
    list.innerHTML = '<li class="muted">No rules yet — pick a show and “Save rule”.</li>';
  }
  for (const r of state.rules) {
    const li = document.createElement("li");
    const name = r.seriesName || r.seriesId;
    const meta = [
      r.action,
      r.fillerRanges ? `filler: ${r.fillerRanges}` : "",
      r.skipRanges ? `skip: ${r.skipRanges}` : "",
      r.nfoRefresh ? "NFO" : "",
    ].filter(Boolean).join(" · ");
    li.innerHTML =
      `<div class="info">` +
      `<div class="title-line"><span class="name" title="${esc(name)}">${esc(name)}</span>` +
      (r.auto ? '<span class="badge schedule">auto</span>' : '<span class="badge dry">manual</span>') +
      `</div>` +
      `<div class="meta" title="${esc(meta)}">${esc(meta)}</div>` +
      `</div>`;
    const run = btn("▶", "Run this rule now", async () => {
      const { runId } = await api(`/api/rules/${r.id}/run`, { method: "POST", body: { dryRun: false } });
      openLog(runId);
      loadRuns();
    });
    const del = btn("✕", "Delete rule", async () => {
      if (!confirm(`Delete rule for ${r.seriesName}?`)) return;
      await api(`/api/rules/${r.id}`, { method: "DELETE" });
      loadRules();
    });
    li.append(run, del);
    list.appendChild(li);
  }
}

async function saveRule() {
  const job = currentJob();
  if (needsRanges(job.action) && !job.fillerRanges) {
    alert("Enter the filler episode numbers first.");
    return;
  }
  const existing = state.rules.find((r) => r.seriesId === job.seriesId && r.action === job.action);
  await api("/api/rules", {
    method: "POST",
    body: {
      ...job,
      id: existing ? existing.id : "",
      nfoRefresh: $("nfoRefresh").checked,
      auto: $("autoRule").checked,
    },
  });
  await loadRules();
}

async function runAllRules(dryRun) {
  if (!state.rules.length) {
    alert("No rules saved yet.");
    return;
  }
  if (!dryRun && !confirm(`Run all ${state.rules.length} rule(s)?`)) return;
  await startRun({
    dryRun,
    nfoRefresh: state.rules.some((r) => r.nfoRefresh),
    label: `all rules (${state.rules.length})`,
    jobs: state.rules.map((r) => ({
      userId: r.userId, seriesId: r.seriesId, seriesName: r.seriesName,
      action: r.action, fillerRanges: r.fillerRanges, skipRanges: r.skipRanges,
    })),
  });
}

const CSV_ACTIONS = { set_index: "set_absolute", tag_filler: "tag_filler", both: "both", untag_filler: "untag_filler" };

async function importCSV() {
  const lines = $("importText").value.split("\n");
  let imported = 0;
  const problems = [];
  for (const raw of lines) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const [seriesId, username, action, ...rest] = line.split(",");
    const mapped = CSV_ACTIONS[(action || "").trim()];
    const user = state.users.find((u) => u.name === (username || "").trim());
    if (!mapped) { problems.push(`unsupported action in: ${line}`); continue; }
    if (!user) { problems.push(`unknown user in: ${line}`); continue; }
    try {
      const item = await api(`/api/users/${user.id}/items/${seriesId.trim()}`);
      await api("/api/rules", {
        method: "POST",
        body: {
          userId: user.id, seriesId: seriesId.trim(), seriesName: item.name,
          action: mapped, fillerRanges: rest.join(",").trim(), nfoRefresh: true, auto: true,
        },
      });
      imported++;
    } catch (e) {
      problems.push(`${line} → ${e.message}`);
    }
  }
  $("importText").value = "";
  await loadRules();
  alert(`Imported ${imported} rule(s).` + (problems.length ? `\n\nProblems:\n${problems.join("\n")}` : ""));
}

// ---------- activity ----------

async function loadRuns() {
  const runs = await api("/api/runs");
  const list = $("runsList");
  list.innerHTML = "";
  if (!runs.length) list.innerHTML = '<li class="muted">No runs yet.</li>';
  for (const r of runs) {
    const li = document.createElement("li");
    const when = new Date(r.startedAt).toLocaleString();
    li.innerHTML =
      `<div class="info">` +
      `<div class="title-line">` +
      `<span class="badge ${r.status}">${r.status}</span>` +
      (r.source === "webhook" ? '<span class="badge webhook">sonarr</span>' : "") +
      (r.source === "schedule" ? '<span class="badge schedule">timer</span>' : "") +
      (r.dryRun ? '<span class="badge dry">dry</span>' : "") +
      `<span class="name" title="${esc(r.label)}">${esc(r.label)}</span>` +
      `</div>` +
      `<div class="meta">${when} · ${r.changed} change(s)${r.errors ? ` · ${r.errors} error(s)` : ""}</div>` +
      `</div>`;
    li.onclick = () => openLog(r.id);
    list.appendChild(li);
  }
}

// ---------- utils ----------

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function pad(n) { return String(n ?? 0).padStart(2, "0"); }
function btn(text, title, onclick) {
  const b = document.createElement("button");
  b.className = "btn small";
  b.textContent = text;
  b.title = title;
  b.onclick = (e) => { e.stopPropagation(); onclick(); };
  return b;
}

// ---------- wire up ----------

$("settingsBtn").onclick = () => {
  const p = $("settingsPanel");
  if (p.hidden) openSettings();
  else p.hidden = true;
};
$("cfgSaveBtn").onclick = () => saveSettings();
$("userSelect").onchange = () => onUserChange().catch(alertErr);
$("viewSelect").onchange = () => onViewChange().catch(alertErr);
$("seriesSearch").oninput = renderSeriesList;
$("fillerRanges").oninput = () => state.episodes.length && renderEpisodes();
$("skipRanges").oninput = () => state.episodes.length && renderEpisodes();
$("previewBtn").onclick = () => runCurrent(true).catch(alertErr);
$("applyBtn").onclick = () => runCurrent(false).catch(alertErr);
$("saveRuleBtn").onclick = () => saveRule().catch(alertErr);
$("runAllDryBtn").onclick = () => runAllRules(true).catch(alertErr);
$("runAllBtn").onclick = () => runAllRules(false).catch(alertErr);
$("importBtn").onclick = () => importCSV().catch(alertErr);
$("refreshRunsBtn").onclick = () => loadRuns().catch(alertErr);
$("logCloseBtn").onclick = () => {
  $("logDrawer").hidden = true;
  state.currentRunId = null;
  clearTimeout(state.pollTimer);
};
function alertErr(e) { alert(e.message); }

loadHealth();
loadUsers();
loadRules().catch(alertErr);
loadRuns().catch(alertErr);
setInterval(loadHealth, 60_000);
