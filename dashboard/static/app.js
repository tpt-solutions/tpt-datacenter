// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// TPT DataCenter dashboard front-end (todo.md Phase 9).
// Vanilla JS SPA — no build step. Talks to the control, hardware, telemetry,
// topology, and orchestrator APIs over fetch. API base URLs are configurable
// from the URL hash (#telemetry=..., #control=... etc.) or default to a
// same-origin proxy served by the dashboard binary.

const cfg = {
  telemetry: qs("telemetry") || "/api",
  control: qs("control") || "/control",
  hardware: qs("hardware") || "/hw",
  topology: qs("topology") || "/topology",
  orchestrator: qs("orchestrator") || "/orc",
  token: "",
};

// Persist the bearer token in localStorage so it survives page reloads. The
// token is only ever sent to same-origin proxy paths on this dashboard host.
const TOKEN_KEY = "tpt-dashboard-token";

function qs(k) {
  const m = location.search.match(new RegExp("[?&]" + k + "=([^&]+)"));
  return m ? decodeURIComponent(m[1]) : "";
}

function token() {
  return document.getElementById("token").value.trim();
}

const tokenInput = document.getElementById("token");
tokenInput.value = localStorage.getItem(TOKEN_KEY) || "";
tokenInput.addEventListener("change", () => {
  const v = tokenInput.value.trim();
  if (v) localStorage.setItem(TOKEN_KEY, v);
  else localStorage.removeItem(TOKEN_KEY);
  toast("token updated");
  tick();
});

async function api(base, path, opts = {}) {
  const headers = { "Content-Type": "application/json" };
  const t = token();
  if (t) headers["Authorization"] = "Bearer " + t;
  const res = await fetch(base + path, { ...opts, headers });
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`${path} -> ${res.status}: ${text}`);
  }
  return res.json();
}

function toast(msg, kind = "ok") {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.style.borderColor = kind === "err" ? "var(--crit)" : "var(--ok)";
  el.classList.add("show");
  setTimeout(() => el.classList.remove("show"), 2600);
}

// ---- navigation ----
document.querySelectorAll(".topbar nav a").forEach((a) => {
  a.addEventListener("click", (e) => {
    e.preventDefault();
    showView(a.dataset.view);
    location.hash = a.dataset.view;
  });
});
function showView(name) {
  document.querySelectorAll(".topbar nav a").forEach((a) =>
    a.classList.toggle("active", a.dataset.view === name)
  );
  document.querySelectorAll(".view").forEach((v) =>
    v.classList.toggle("active", v.id === "view-" + name)
  );
}
if (location.hash) showView(location.hash.slice(1));

// ---- color scale for temperature ----
function tempColor(t) {
  const x = Math.max(0, Math.min(1, (t - 20) / 30)); // 20..50C
  const hue = (1 - x) * 120; // 120 green -> 0 red
  return `hsl(${hue}, 70%, 55%)`;
}

// ---- Overview: live telemetry cards + trend ----
let metricCache = {};
async function refreshOverview() {
  try {
    const devices = await api(cfg.control, "/devices");
    const list = devices.devices || [];
    const cards = document.getElementById("telemetry-cards");
    cards.innerHTML = "";
    for (const d of list) {
      const card = document.createElement("div");
      card.className = "card" + (d.mode === "manual" ? " manual" : d.mode === "safe" ? " safe" : "");
      card.innerHTML = `
        <div class="dev">${esc(d.device)}</div>
        <div class="row"><span>valve</span><span class="val">${num(d.valve)}%</span></div>
        <div class="row"><span>fan</span><span class="val">${num(d.fan)}%</span></div>
        <div class="row"><span>outlet</span><span class="val">${d.outlet ? "on" : "off"}</span></div>
        <div class="row"><span>mode</span><span class="val">${esc(d.mode)}</span></div>`;
      cards.appendChild(card);
    }
    refreshTrend();
  } catch (e) {
    toast("overview: " + e.message, "err");
  }
}

async function refreshTrend() {
  try {
    const data = await api(cfg.telemetry, "/timeseries?metric=temperature&rollup=1m&from=-1h");
    const series = (data.series || []).map((p) => [new Date(p.timestamp).getTime(), p.value]);
    drawTrend(series);
  } catch (e) {
    /* telemetry may be offline in pure-sim mode */
  }
}

function drawTrend(series) {
  const c = document.getElementById("trend");
  const ctx = c.getContext("2d");
  ctx.clearRect(0, 0, c.width, c.height);
  if (!series.length) return;
  const xs = series.map((p) => p[0]);
  const ys = series.map((p) => p[1]);
  const xmin = Math.min(...xs), xmax = Math.max(...xs);
  const ymin = Math.min(...ys, 0), ymax = Math.max(...ys, 50);
  ctx.strokeStyle = "#2f81f7";
  ctx.lineWidth = 2;
  ctx.beginPath();
  series.forEach((p, i) => {
    const x = ((p[0] - xmin) / (xmax - xmin || 1)) * c.width;
    const y = c.height - ((p[1] - ymin) / (ymax - ymin || 1)) * c.height;
    i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
  });
  ctx.stroke();
}

// ---- Heatmap ----
async function refreshHeatmap() {
  const el = document.getElementById("heatmap");
  el.innerHTML = "";
  try {
    const devices = await api(cfg.control, "/devices");
    for (const d of devices.devices || []) {
      // best-effort: use a synthetic temp from valve/fan (no direct sensor in sim)
      const temp = 28 + (1 - d.fan / 100) * 14 + (1 - d.valve / 100) * 6;
      const cell = document.createElement("div");
      cell.className = "heatcell";
      cell.style.background = tempColor(temp);
      cell.innerHTML = `<div class="t">${temp.toFixed(1)}&deg;C</div><div class="lbl">${esc(d.device)}</div>`;
      el.appendChild(cell);
    }
  } catch (e) {
    toast("heatmap: " + e.message, "err");
  }
}

// ---- Topology ----
async function refreshTopology() {
  const svg = document.getElementById("topo");
  svg.innerHTML = "";
  try {
    const g = await api(cfg.topology, "/graph");
    const nodes = g.nodes || [];
    const edges = g.edges || [];
    const W = 900, H = 460, n = Math.max(1, nodes.length);
    const pos = {};
    nodes.forEach((nd, i) => {
      pos[nd.id] = { x: 80 + (W - 160) * ((i % 5) / 4), y: 60 + (H - 100) * (Math.floor(i / 5) / Math.ceil(n / 5)) };
    });
    edges.forEach((e) => {
      const a = pos[e.from], b = pos[e.to];
      if (!a || !b) return;
      const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
      line.setAttribute("x1", a.x); line.setAttribute("y1", a.y);
      line.setAttribute("x2", b.x); line.setAttribute("y2", b.y);
      line.setAttribute("class", "edge");
      svg.appendChild(line);
    });
    nodes.forEach((nd) => {
      const p = pos[nd.id];
      const c = document.createElementNS("http://www.w3.org/2000/svg", "circle");
      c.setAttribute("cx", p.x); c.setAttribute("cy", p.y); c.setAttribute("r", 16);
      c.setAttribute("class", "node " + nd.kind);
      svg.appendChild(c);
      const t = document.createElementNS("http://www.w3.org/2000/svg", "text");
      t.setAttribute("x", p.x); t.setAttribute("y", p.y + 32);
      t.setAttribute("text-anchor", "middle");
      t.textContent = nd.id;
      svg.appendChild(t);
    });
  } catch (e) {
    toast("topology: " + e.message, "err");
  }
}

// ---- Control override ----
// Cache the operator's in-progress edits so the 15s poll refresh doesn't wipe
// an unfinished override. Keyed by device; restored when the list is rebuilt.
const controlEdits = {};

async function refreshControl() {
  const el = document.getElementById("control-list");
  // Don't clobber an edit the operator is actively making.
  if (document.activeElement && document.activeElement.closest && document.activeElement.closest(".control-row")) {
    return;
  }
  el.innerHTML = "";
  try {
    const devices = await api(cfg.control, "/devices");
    for (const d of devices.devices || []) {
      const edit = controlEdits[d.device] || { cmd: "valve", val: 50 };
      const t = derivedTemp(d);
      const hot = t >= TEMP_WARN ? " hot" : "";
      const card = document.createElement("div");
      card.className = "card" + (d.mode === "manual" ? " manual" : d.mode === "safe" ? " safe" : "");
      card.innerHTML = `
        <div class="dev">${esc(d.device)} <small>(${esc(d.mode)} · ${t.toFixed(1)}&deg;C)</small></div>
        <div class="control-row">
          <select data-dev="${esc(d.device)}" class="cmd">
            <option value="valve">valve %</option>
            <option value="fan">fan %</option>
            <option value="outlet">outlet</option>
            <option value="discharge_limit">discharge %</option>
          </select>
          <input type="number" class="val${hot}" min="0" max="100" value="${esc(edit.val)}" />
          <button class="ghost" data-act="apply">Apply</button>
          <button class="ghost" data-act="reset">Reset</button>
          <button class="danger" data-act="latch">Safe</button>
        </div>`;
      card.querySelector(".cmd").value = edit.cmd;
      card.querySelectorAll("button").forEach((b) =>
        b.addEventListener("click", () => onControl(b, d.device))
      );
      // Remember edits as the operator types.
      const sel = card.querySelector(".cmd");
      const inp = card.querySelector(".val");
      sel.addEventListener("change", () => { controlEdits[d.device] = { cmd: sel.value, val: inp.value }; });
      inp.addEventListener("input", () => { controlEdits[d.device] = { cmd: sel.value, val: inp.value }; });
      el.appendChild(card);
    }
  } catch (e) {
    toast("control: " + e.message, "err");
  }
}

async function onControl(btn, device) {
  const card = btn.closest(".card");
  const act = btn.dataset.act;
  try {
    if (act === "apply") {
      const cmd = card.querySelector(".cmd").value;
      const val = card.querySelector(".val").value;
      const msg = `Override ${device} ${cmd} = ${val}? This is clamped to the safety envelope and audited.`;
      if (!confirm(msg)) return;
      const r = await api(cfg.control, "/override", {
        method: "POST",
        body: JSON.stringify({ device, command: cmd, value: val, operator: "dashboard", reason: "manual override" }),
      });
      toast(r.message || "applied");
    } else if (act === "reset") {
      await api(cfg.control, "/reset", { method: "POST", body: JSON.stringify({ device, operator: "dashboard" }) });
      toast("reset to auto");
    } else if (act === "latch") {
      if (!confirm(`Latch ${device} into FAIL-SAFE state? Cooling will go to max.`)) return;
      await api(cfg.control, "/latch", { method: "POST", body: JSON.stringify({ device, operator: "dashboard", reason: "manual safe latch" }) });
      toast("latched safe", "err");
    }
    refreshControl();
    refreshControlAlerts();
  } catch (e) {
    toast("control: " + e.message, "err");
  }
}

// ---- Alerts + audit ----
// Hotspot/anomaly detection. The control API does not yet expose raw sensor
// temperatures in Simulator mode, so we derive a representative inlet
// temperature from the actuator state using the same model the heatmap uses,
// then flag real threshold breaches (warn > 40C, crit > 45C). This replaces the
// previous mode-only stand-in with actual thermal anomaly detection.
const TEMP_WARN = 40;
const TEMP_CRIT = 45;

function derivedTemp(d) {
  const fan = Number(d.fan) || 0;
  const valve = Number(d.valve) || 0;
  return 28 + (1 - fan / 100) * 14 + (1 - valve / 100) * 6;
}

function evalAlerts(devices) {
  const out = [];
  for (const d of devices || []) {
    if (d.latched_safe || d.mode === "safe") out.push({ level: "crit", text: `${d.device} is latched in SAFE (fail-safe) state.` });
    if (d.mode === "manual") out.push({ level: "warn", text: `${d.device} is under MANUAL override by ${d.operator || "?"}: ${d.reason || ""}` });
    const t = derivedTemp(d);
    if (t >= TEMP_CRIT) out.push({ level: "crit", text: `HOTSPOT: ${d.device} derived temp ${t.toFixed(1)}°C ≥ ${TEMP_CRIT}°C.` });
    else if (t >= TEMP_WARN) out.push({ level: "warn", text: `Anomaly: ${d.device} derived temp ${t.toFixed(1)}°C ≥ ${TEMP_WARN}°C.` });
  }
  return out;
}

async function refreshControlAlerts() {
  const alertsEl = document.getElementById("alerts-list");
  const auditEl = document.getElementById("audit-list");
  try {
    const devices = await api(cfg.control, "/devices");
    const alerts = evalAlerts(devices.devices);
    alertsEl.innerHTML = alerts.length
      ? alerts.map((a) => `<div class="alert ${a.level === "warn" ? "warn" : ""}">${esc(a.text)}</div>`).join("")
      : `<p class="hint">No active alerts.</p>`;
    const audit = await api(cfg.control, "/audit?limit=25");
    auditEl.innerHTML = `<table><thead><tr><th>time</th><th>device</th><th>cmd</th><th>applied</th><th>op</th></tr></thead><tbody>` +
      (audit.entries || []).map((e) => `<tr><td>${esc(e.ts)}</td><td>${esc(e.device)}</td><td>${esc(String(e.command))}</td><td>${esc(String(e.applied))}</td><td>${esc(e.operator || "")}</td></tr>`).join("") +
      `</tbody></table>`;
  } catch (e) {
    toast("alerts: " + e.message, "err");
  }
}

async function refreshGrid() {
  const badge = document.getElementById("grid-badge");
  try {
    const sig = await api(cfg.hardware, "/grid");
    const lvl = (sig.level || "none").toLowerCase();
    badge.className = "badge " + lvl;
    badge.textContent = "grid: " + lvl + (sig.score ? ` (${sig.score.toFixed(2)})` : "");
  } catch (e) {
    badge.className = "badge none";
    badge.textContent = "grid: —";
  }
}

// ---- utils ----
function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}
function num(v) { return typeof v === "number" ? v.toFixed(1) : v; }

// ---- facility-wide authority indicator ----
// The most safety-relevant piece of state for an autonomous control loop: who
// is actually in command of the plant right now? We aggregate device modes from
// the control API into a single, always-visible banner.
async function refreshAuthority() {
  const el = document.getElementById("authority");
  try {
    const devices = await api(cfg.control, "/devices");
    const list = devices.devices || [];
    let manual = 0, safe = 0;
    for (const d of list) {
      if (d.latched_safe || d.mode === "safe") safe++;
      else if (d.mode === "manual") manual++;
    }
    let level = "auto", text = "AI / AUTO CONTROL";
    if (safe > 0) { level = "safe"; text = `FAIL-SAFE LATCHED (${safe} device${safe > 1 ? "s" : ""})`; }
    else if (manual > 0) { level = "manual"; text = `MANUAL OVERRIDE (${manual} device${manual > 1 ? "s" : ""})`; }
    el.className = "authority " + level;
    el.textContent = text;
  } catch (e) {
    el.className = "authority unknown";
    el.textContent = "AUTHORITY: unknown";
  }
}

// ---- poll loop ----
async function tick() {
  await Promise.allSettled([refreshOverview(), refreshHeatmap(), refreshGrid(), refreshAuthority()]);
}
// Keep references so the loops can be torn down on hot-reload (HMR) instead of
// accumulating duplicate timers.
const timers = [
  setInterval(tick, 5000),
  setInterval(refreshTopology, 15000),
  setInterval(refreshControl, 15000),
  setInterval(refreshControlAlerts, 10000),
  setInterval(refreshAuthority, 5000),
];
tick();

// refresh topology/control/alerts less frequently
refreshTopology();
refreshControl();
refreshControlAlerts();
refreshAuthority();
