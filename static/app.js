'use strict';

// ── State ────────────────────────────────────────────────────────────────────
const state = {
  pools: [],
  poolStatuses: [],
  version: '',
  sysinfo: null,
  datasets: [],
  snapshots: [],
  iostat: [],
  smart: null,
  users: [],
  groups: [],
  sambaUsers: [],
  sambaAvailable: false,
  smbShares: [],      // [{name, path}] from net usershare info
  smbHomes: { enabled: false, path: '', browseable: 'no', read_only: 'no', create_mask: '0644', directory_mask: '0755' },
  timeMachineShares: [],  // [{name, path, max_size, valid_users}]
  iscsiTargets: [],   // [{iqn, zvol_name, ...}] from /api/iscsi-targets
  activeTab: 'pools',
  collapsedDatasets: new Set(),
  aclDataset: '',
  aclData: null,
  aclStatus: {},
  hideSystemUsers: true,
  hideSystemGroups: true,
  scrubSchedules: {},      // pool name → ScrubSchedule
  scrubScheduleMode: 'cron',    // "cron" | "periodic"
  scrubThresholdDays: 35,       // FreeBSD periodic threshold (global)
  autoSnapshot: {},             // dataset name → AutoSnapshotProps
  selectedSnaps: new Set(),     // full snapshot names checked for batch delete
  schema: null,                 // GET /api/schema response
};

// ── Reactive store ──────────────────────────────────────────────────────────
const _subs = {};          // key → Set<fn>
let _batching = false;
let _dirty = new Set();

function subscribe(keys, fn) {
  for (const k of keys) {
    if (!_subs[k]) _subs[k] = new Set();
    _subs[k].add(fn);
  }
}

function storeSet(key, value) {
  if (state[key] === value) return;
  state[key] = value;
  if (_batching) { _dirty.add(key); return; }
  _flush(new Set([key]));
}

function storeBatch(fn) {
  _batching = true;
  _dirty = new Set();
  try { fn(); } finally {
    _batching = false;
    const keys = _dirty;
    _dirty = new Set();
    if (keys.size) _flush(keys);
  }
}

function _flush(keys) {
  const fns = new Set();
  for (const k of keys) {
    if (_subs[k]) for (const fn of _subs[k]) fns.add(fn);
  }
  for (const fn of fns) fn();
}

// ── API helpers ───────────────────────────────────────────────────────────────
async function api(method, path, body) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const data = await res.json();
  if (!res.ok) {
    const err = new Error(data.error || `HTTP ${res.status}`);
    err.tasks = data.tasks || null;
    throw err;
  }
  return data;
}

// ── Formatting ────────────────────────────────────────────────────────────────
function fmtBytes(n) {
  if (n === 0) return '—';
  const units = ['B', 'K', 'M', 'G', 'T', 'P'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return v.toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
}

function fmtDate(epoch) {
  if (!epoch) return '—';
  return new Date(epoch * 1000).toLocaleString();
}

function fmtPct(p) {
  return p.toFixed(1) + '%';
}

// ── Operation log dialog ──────────────────────────────────────────────────────
const opLogDialog = document.getElementById('opLogDialog');
document.getElementById('opLogClose').addEventListener('click', () => opLogDialog.close());

const stepIcons = { ok: '✓', changed: '●', failed: '✗', skipped: '–' };

function showOpLogRunning(title) {
  document.getElementById('opLogTitle').textContent = title;
  document.getElementById('opLogSteps').innerHTML = `
    <div class="op-step running">
      <span class="op-step-icon">⟳</span>
      <span class="op-step-name">Running…</span>
    </div>`;
  document.getElementById('opLogError').style.display = 'none';
  document.getElementById('opLogClose').disabled = true;
  if (!opLogDialog.open) opLogDialog.showModal();
}

function showOpLog(title, tasks, errorMsg) {
  document.getElementById('opLogTitle').textContent = title;
  const stepsEl = document.getElementById('opLogSteps');
  const errorEl = document.getElementById('opLogError');

  stepsEl.innerHTML = (tasks || []).map(t => `
    <div class="op-step ${esc(t.status)}">
      <span class="op-step-icon">${esc(stepIcons[t.status] || '?')}</span>
      <span class="op-step-name">${esc(t.name)}</span>
      ${t.msg ? `<span class="op-step-msg">${esc(t.msg)}</span>` : ''}
    </div>`).join('');

  if (errorMsg) {
    errorEl.textContent = errorMsg;
    errorEl.style.display = '';
  } else {
    errorEl.style.display = 'none';
  }
  document.getElementById('opLogClose').disabled = false;
  if (!opLogDialog.open) opLogDialog.showModal();
}

// Append a single task step to the op-log while it is in running state.
// Removes the "Running…" placeholder on the first call.
function appendOpLogStep(step) {
  const stepsEl = document.getElementById('opLogSteps');
  // Remove the "Running…" placeholder if present
  const running = stepsEl.querySelector('.op-step.running');
  if (running) running.remove();
  const div = document.createElement('div');
  div.className = `op-step ${esc(step.status)}`;
  div.innerHTML = `
    <span class="op-step-icon">${esc(stepIcons[step.status] || '?')}</span>
    <span class="op-step-name">${esc(step.name)}</span>
    ${step.msg ? `<span class="op-step-msg">${esc(step.msg)}</span>` : ''}`;
  stepsEl.appendChild(div);
}

// ── Toast ─────────────────────────────────────────────────────────────────────
let toastTimer;
function toast(msg, type = 'ok') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = `toast show ${type}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = 'toast'; }, 3500);
}

// ── Tabs ──────────────────────────────────────────────────────────────────────
document.querySelectorAll('.tab-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-pane').forEach(p => p.classList.remove('active'));
    btn.classList.add('active');
    state.activeTab = btn.dataset.tab;
    document.getElementById('tab-' + state.activeTab).classList.add('active');
  });
});

// ── Refresh ───────────────────────────────────────────────────────────────────
document.getElementById('refreshBtn').addEventListener('click', () => loadAll());

function setRefreshing(v) {
  document.getElementById('refreshBtn').classList.toggle('spinning', v);
}

// ── Schema helpers ────────────────────────────────────────────────────────────
// Populate all schema-driven <select> elements from state.schema.
// Called once after loadAll() sets state.schema.
function buildFormSelects() {
  const props = state.schema?.dataset_properties || [];
  for (const prop of props) {
    if (prop.input_type !== 'select') continue;
    const opts = prop.options.map(o => {
      const sel = o.default ? ' selected' : '';
      return `<option value="${esc(o.value)}"${sel}>${esc(o.label)}</option>`;
    }).join('');
    if (prop.create) {
      const el = document.getElementById('ds-' + prop.name);
      if (el) el.innerHTML = opts;
    }
    if (prop.editable) {
      const el = document.getElementById('edit-ds-' + prop.name);
      if (el) el.innerHTML = opts;
    }
  }

  const shells = state.schema?.user_shells || [];
  if (shells.length) {
    const opts = shells.map(s => `<option value="${esc(s)}">${esc(s)}</option>`).join('');
    const create = document.getElementById('user-shell');
    if (create) create.innerHTML = opts;
    const edit = document.getElementById('edit-user-shell');
    if (edit) edit.innerHTML = opts;
  }
}

// ── Load all ──────────────────────────────────────────────────────────────────
// Fast path: all endpoints that respond in <100 ms. Rendered immediately.
// Slow path: iostat (~1 s sampling) and SMART (drive scans) load separately
// so they never block the initial page render.
async function loadAll() {
  setRefreshing(true);
  try {
    // Use null as the sentinel for failed fetches so we can distinguish
    // "endpoint returned empty" from "fetch failed" and preserve last-known-good state.
    const [pools, poolStatuses, version, sysinfo, datasets, snapshots, users, groups, smbData, smbShares, smbHomes, tmShares, iscsiTargets, scrubSchedules, autoSnapshotSchedules, schema] = await Promise.all([
      api('GET', '/api/pools').catch(() => null),
      api('GET', '/api/poolstatus').catch(() => null),
      api('GET', '/api/version').catch(() => null),
      api('GET', '/api/sysinfo').catch(() => null),
      api('GET', '/api/datasets').catch(() => null),
      api('GET', '/api/snapshots').catch(() => null),
      api('GET', '/api/users').catch(() => null),
      api('GET', '/api/groups').catch(() => null),
      api('GET', '/api/smb-users').catch(() => null),
      api('GET', '/api/smb-shares').catch(() => null),
      api('GET', '/api/smb/homes').catch(() => null),
      api('GET', '/api/smb/timemachine').catch(() => null),
      api('GET', '/api/iscsi-targets').catch(() => null),
      api('GET', '/api/scrub-schedules').catch(() => null),
      api('GET', '/api/auto-snapshot-schedules').catch(() => null),
      api('GET', '/api/schema').catch(() => null),
    ]);
    storeBatch(() => {
      if (pools !== null) storeSet('pools', pools);
      if (poolStatuses !== null) storeSet('poolStatuses', poolStatuses);
      if (version !== null) storeSet('version', version?.version || '');
      if (sysinfo !== null) storeSet('sysinfo', sysinfo);
      if (datasets !== null) storeSet('datasets', datasets);
      if (snapshots !== null) storeSet('snapshots', snapshots);
      if (users !== null) storeSet('users', users);
      if (groups !== null) storeSet('groups', groups);
      if (smbData !== null) {
        storeSet('sambaAvailable', smbData?.available ?? false);
        storeSet('sambaUsers', smbData?.users || []);
      }
      if (smbShares !== null) storeSet('smbShares', smbShares);
      if (smbHomes !== null) storeSet('smbHomes', smbHomes);
      if (tmShares !== null) storeSet('timeMachineShares', tmShares);
      if (iscsiTargets !== null) storeSet('iscsiTargets', iscsiTargets);
      if (scrubSchedules !== null) {
        const schedData = scrubSchedules || { mode: 'cron', schedules: [] };
        storeSet('scrubScheduleMode', schedData.mode || 'cron');
        storeSet('scrubThresholdDays', schedData.threshold_days || 35);
        storeSet('scrubSchedules', Object.fromEntries((schedData.schedules || []).map(s => [s.pool, s])));
      }
      if (autoSnapshotSchedules !== null) storeSet('autoSnapshot', autoSnapshotSchedules);
      if (schema !== null) storeSet('schema', schema);
    });
  } catch (e) {
    toast('Load failed: ' + e.message, 'err');
    console.error(e);
  } finally {
    setRefreshing(false);
  }
  refreshACLStatus();
  loadSlowMetrics();
}

// Loads iostat (~1 s) and SMART (drive scans) without blocking the main render.
async function loadSlowMetrics() {
  const [iostat, smart] = await Promise.all([
    api('GET', '/api/iostat').catch(() => null),
    api('GET', '/api/smart').catch(() => null),
  ]);
  storeBatch(() => {
    if (iostat !== null) storeSet('iostat', iostat);
    if (smart !== null) storeSet('smart', smart);
  });
}

// ── Formatting: uptime ────────────────────────────────────────────────────────
function fmtUptime(secs) {
  const s = Math.round(secs);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// ── Render: System info ────────────────────────────────────────────────────────
function renderSysInfo() {
  const wrap = document.getElementById('sysinfo-wrap');
  if (!wrap) return;
  const s = state.sysinfo;
  if (!s) { wrap.innerHTML = ''; return; }

  const loadClass = s.load1 > s.cpu_count * 0.9 ? 'load-high'
                  : s.load1 > s.cpu_count * 0.6 ? 'load-mid' : '';

  // Header version badge
  const verBadge = document.getElementById('appVersion');
  if (verBadge && s.app_version) verBadge.textContent = s.app_version;

  wrap.innerHTML = `
    <div class="sysinfo-card">
      <div class="sysinfo-section-label">Host</div>
      <div class="sysinfo-grid">
        <div class="si-item"><div class="si-label">Hostname</div><div class="si-value">${esc(s.hostname)}</div></div>
        <div class="si-item"><div class="si-label">OS</div><div class="si-value">${esc(s.os)}/${esc(s.arch)}</div></div>
        <div class="si-item"><div class="si-label">Kernel</div><div class="si-value">${esc(s.kernel)}</div></div>
        <div class="si-item"><div class="si-label">Uptime</div><div class="si-value">${s.uptime_secs ? fmtUptime(s.uptime_secs) : '—'}</div></div>
        <div class="si-item"><div class="si-label">CPUs</div><div class="si-value">${s.cpu_count}</div></div>
        <div class="si-item"><div class="si-label">Load 1m/5m/15m</div><div class="si-value ${loadClass}">${s.load1.toFixed(2)} / ${s.load5.toFixed(2)} / ${s.load15.toFixed(2)}</div></div>
      </div>
      <div class="sysinfo-section-label" style="margin-top:0.75rem">Process</div>
      <div class="sysinfo-grid">
        <div class="si-item"><div class="si-label">PID</div><div class="si-value">${s.pid}</div></div>
        <div class="si-item"><div class="si-label">Uptime</div><div class="si-value">${fmtUptime(s.proc_uptime_secs)}</div></div>
        <div class="si-item"><div class="si-label">Heap</div><div class="si-value">${s.heap_alloc_mb.toFixed(1)} MB</div></div>
        <div class="si-item"><div class="si-label">Sys mem</div><div class="si-value">${s.sys_mb.toFixed(1)} MB</div></div>
        <div class="si-item"><div class="si-label">Goroutines</div><div class="si-value">${s.goroutines}</div></div>
        <div class="si-item"><div class="si-label">GC cycles</div><div class="si-value">${s.num_gc}</div></div>
      </div>
    </div>`;
}

// ── Render: Software ──────────────────────────────────────────────────────────
function renderSoftware() {
  const wrap = document.getElementById('software-wrap');
  if (!wrap) return;
  const tools = state.sysinfo?.software;
  if (!tools?.length) { wrap.innerHTML = ''; return; }

  const rows = tools.map(t => {
    const na = !t.version;
    return `<tr>
      <td class="mono">${esc(t.name)}</td>
      <td class="${na ? 'sw-na' : 'mono'}">${na ? 'N/A' : esc(t.version)}</td>
    </tr>`;
  }).join('');

  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Tool</th><th>Version / Status</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
}

// ── Render: Pools ─────────────────────────────────────────────────────────────
function renderPools() {
  const grid = document.getElementById('pools-grid');

if (!state.pools.length) {
    grid.innerHTML = '<div class="loading">No pools found.</div>';
    return;
  }

  // Build a lookup map: pool name → PoolDetail
  const statusMap = {};
  for (const d of state.poolStatuses) statusMap[d.name] = d;

  grid.innerHTML = state.pools.map(p => {
    const pct = p.used_percent;
    const barClass = pct > 90 ? 'crit' : pct > 75 ? 'warn' : '';
    const detail = statusMap[p.name];

    const scrubState = detail?.scan ? scrubStateOf(detail.scan) : 'idle';
    const scanLine = detail?.scan
      ? `<div class="pool-scan">${esc(detail.scan)}</div>`
      : '';

    const sched = state.scrubSchedules[p.name];
    const allDefault = Object.keys(state.scrubSchedules).length === 0;
    const badgeText = fmtScrubScheduleBadge(state.scrubScheduleMode, !!sched, allDefault, state.scrubThresholdDays);
    const schedBadge = badgeText
      ? `<span class="scrub-schedule-badge">${esc(badgeText)}</span>`
      : `<span class="scrub-schedule-badge muted">No schedule</span>`;

    const scrubActions = `
      <div class="pool-scrub-actions">
        ${scrubState === 'in_progress' || scrubState === 'paused'
          ? `<button class="btn-secondary btn-sm" onclick="cancelScrub('${p.name}')">Cancel Scrub</button>`
          : `<button class="btn-secondary btn-sm" onclick="startScrub('${p.name}')">Start Scrub</button>`
        }
        <button class="btn-secondary btn-sm" onclick="openScrubScheduleDialog('${p.name}')">Schedule&hellip;</button>
        ${schedBadge}
      </div>`;

    const statusLine = detail?.status
      ? `<div class="pool-status-msg">${esc(detail.status)}</div>`
      : '';

    const errLine = detail?.errors && detail.errors !== 'No known data errors'
      ? `<div class="pool-errors">${esc(detail.errors)}</div>`
      : '';

    const vdevRows = (detail?.vdevs || [])
      .filter(v => v.depth > 0)   // skip the root pool entry (depth 0)
      .map(v => {
        const indent = v.depth - 1;
        const errs = v.read || v.write || v.cksum
          ? `<span class="vdev-errs">${v.read}/${v.write}/${v.cksum}</span>`
          : '';
        return `
          <div class="vdev-row" style="--vdepth:${indent}">
            <span class="vdev-name">${esc(v.name)}</span>
            <span class="vdev-state state-${esc(v.state || 'UNKNOWN')}">${esc(v.state || '—')}</span>
            ${errs}
          </div>`;
      }).join('');

    const vdevSection = vdevRows
      ? `<div class="pool-vdevs"><div class="pool-vdevs-label">Devices</div>${vdevRows}</div>`
      : '';

    return `
      <div class="pool-card">
        <div class="pool-card-header">
          <span class="pool-name">${esc(p.name)}</span>
          <span class="health-badge health-${esc(p.health)}">${esc(p.health)}</span>
        </div>
        <div class="pool-bar-wrap">
          <div class="pool-bar ${barClass}" style="width:${Math.min(pct,100).toFixed(1)}%"></div>
        </div>
        <div class="pool-stats">
          <div class="stat-item">
            <div class="stat-label">Total</div>
            <div class="stat-value">${fmtBytes(p.size)}</div>
          </div>
          <div class="stat-item">
            <div class="stat-label">Used</div>
            <div class="stat-value">${fmtBytes(p.alloc)}</div>
          </div>
          <div class="stat-item">
            <div class="stat-label">Free</div>
            <div class="stat-value">${fmtBytes(p.free)}</div>
          </div>
          <div class="stat-item">
            <div class="stat-label">Used%</div>
            <div class="stat-value">${fmtPct(pct)}</div>
          </div>
          <div class="stat-item">
            <div class="stat-label">Frag</div>
            <div class="stat-value">${esc(p.frag)}</div>
          </div>
          <div class="stat-item">
            <div class="stat-label">Dedup</div>
            <div class="stat-value">${esc(p.dedup)}</div>
          </div>
        </div>
        ${scanLine}${scrubActions}${statusLine}${errLine}${vdevSection}
      </div>`;
  }).join('');
}

// ── Pool scrub helpers ────────────────────────────────────────────────────────
// Returns 'in_progress', 'paused', or 'idle' based on the raw scan string.
function scrubStateOf(scan) {
  if (!scan) return 'idle';
  if (scan.startsWith('scrub in progress')) return 'in_progress';
  if (scan.startsWith('scrub paused')) return 'paused';
  return 'idle';
}

async function startScrub(pool) {
  showOpLogRunning(`Start scrub: ${pool}`);
  try {
    const data = await api('POST', `/api/scrub/${encodeURIComponent(pool)}`);
    showOpLog(`Start scrub: ${pool}`, data.tasks, null);
    toast(`Scrub started on ${pool}`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(`Start scrub: ${pool}`, err.tasks, err.message);
  }
}

async function cancelScrub(pool) {
  showOpLogRunning(`Cancel scrub: ${pool}`);
  try {
    const data = await api('DELETE', `/api/scrub/${encodeURIComponent(pool)}`);
    showOpLog(`Cancel scrub: ${pool}`, data.tasks, null);
    toast(`Scrub cancelled on ${pool}`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(`Cancel scrub: ${pool}`, err.tasks, err.message);
  }
}

// ── Scrub schedule helpers ────────────────────────────────────────────────────
// Returns badge text, or null if pool has no schedule.
// allDefault = the pools list is empty (platform scrubs all pools by default).
function fmtScrubScheduleBadge(mode, inList, allDefault, thresholdDays) {
  if (!inList && !allDefault) return null;
  if (mode === 'periodic') return `Scrub: every ${thresholdDays ?? 35}d`;
  return 'Scrub: 2nd Sun'; // zfsutils-linux
}

let _scrubSchedulePool = '';

function openScrubScheduleDialog(pool) {
  _scrubSchedulePool = pool;
  const sched = state.scrubSchedules[pool];
  const periodic = state.scrubScheduleMode === 'periodic';
  const allDefault = Object.keys(state.scrubSchedules).length === 0;
  document.getElementById('scrubSchedulePool').textContent = pool;

  document.getElementById('scrubCronRows').style.display = 'none'; // unused
  document.getElementById('scrubPeriodicRow').style.display = periodic ? '' : 'none';
  document.getElementById('scrubZfsutilsRow').style.display = periodic ? 'none' : '';

  if (periodic) {
    document.getElementById('scrubScheduleThreshold').value = state.scrubThresholdDays ?? 35;
  } else {
    const statusEl = document.getElementById('scrubZfsutilsStatus');
    if (allDefault) {
      statusEl.textContent = 'All pools are scrubbed on the 2nd Sunday monthly (ZFS_SCRUB_POOLS is empty — package default).';
    } else if (sched) {
      statusEl.textContent = 'Pool is explicitly listed in ZFS_SCRUB_POOLS.';
    } else {
      statusEl.textContent = 'Pool is not in ZFS_SCRUB_POOLS and will not be scrubbed automatically.';
    }
  }

  document.getElementById('scrubScheduleSaveBtn').textContent = sched ? 'Update' : 'Enable';
  document.getElementById('scrubScheduleRemoveBtn').style.display = sched ? '' : 'none';
  document.getElementById('scrubScheduleDialog').showModal();
}

async function _refreshScrubSchedules() {
  const data = await api('GET', '/api/scrub-schedules').catch(() => null);
  if (data) {
    storeBatch(() => {
      storeSet('scrubScheduleMode', data.mode || 'zfsutils');
      storeSet('scrubThresholdDays', data.threshold_days || 35);
      storeSet('scrubSchedules', Object.fromEntries((data.schedules || []).map(s => [s.pool, s])));
    });
  }
}

async function saveScrubSchedule() {
  const body = state.scrubScheduleMode === 'periodic'
    ? { threshold_days: parseInt(document.getElementById('scrubScheduleThreshold').value, 10) || 35 }
    : {};
  document.getElementById('scrubScheduleDialog').close();
  showOpLogRunning(`Enable scrub: ${_scrubSchedulePool}`);
  try {
    const data = await api('PUT', `/api/scrub-schedule/${encodeURIComponent(_scrubSchedulePool)}`, body);
    showOpLog(`Enable scrub: ${_scrubSchedulePool}`, data.tasks, null);
    toast(`Scrub enabled for ${_scrubSchedulePool}`, 'ok');
    await _refreshScrubSchedules();
  } catch (err) {
    showOpLog(`Enable scrub: ${_scrubSchedulePool}`, err.tasks, err.message);
  }
}

async function removeScrubSchedule() {
  document.getElementById('scrubScheduleDialog').close();
  showOpLogRunning(`Remove scrub: ${_scrubSchedulePool}`);
  try {
    const data = await api('DELETE', `/api/scrub-schedule/${encodeURIComponent(_scrubSchedulePool)}`);
    showOpLog(`Remove scrub: ${_scrubSchedulePool}`, data.tasks, null);
    toast(`Scrub schedule removed for ${_scrubSchedulePool}`, 'ok');
    await _refreshScrubSchedules();
  } catch (err) {
    showOpLog(`Remove scrub: ${_scrubSchedulePool}`, err.tasks, err.message);
  }
}

// ── Auto-snapshot schedule helpers ────────────────────────────────────────────
const _autoSnapPeriods = [
  { prop: 'com.sun:auto-snapshot:frequent', label: 'Frequent (15 min)', id: 'autosnap-frequent' },
  { prop: 'com.sun:auto-snapshot:hourly',   label: 'Hourly',            id: 'autosnap-hourly'   },
  { prop: 'com.sun:auto-snapshot:daily',    label: 'Daily',             id: 'autosnap-daily'    },
  { prop: 'com.sun:auto-snapshot:weekly',   label: 'Weekly',            id: 'autosnap-weekly'   },
  { prop: 'com.sun:auto-snapshot:monthly',  label: 'Monthly',           id: 'autosnap-monthly'  },
];

let _autoSnapDataset = '';

async function openAutoSnapDialog(name) {
  _autoSnapDataset = name;
  document.getElementById('autoSnapDatasetName').textContent = name;
  // Reset to blank while loading
  document.getElementById('autosnap-master').value = '';
  _autoSnapPeriods.forEach(p => {
    document.getElementById(p.id).value = '';
    const hint = document.getElementById(p.id + '-hint');
    if (hint) hint.textContent = '';
  });
  document.getElementById('autoSnapDialog').showModal();
  try {
    const encodedName = name.split('/').map(encodeURIComponent).join('/');
    const props = await api('GET', '/api/auto-snapshot/' + encodedName);
    state.autoSnapshot[name] = props;
    _populateAutoSnapDialog(props);
  } catch (e) {
    document.getElementById('autoSnapDialog').close();
    toast('Failed to load auto-snapshot config: ' + e.message, 'err');
  }
}

function _populateAutoSnapDialog(props) {
  const master = props['com.sun:auto-snapshot'];
  const masterEl = document.getElementById('autosnap-master');
  masterEl.value = master?.source === 'local' ? master.value : '';

  _autoSnapPeriods.forEach(p => {
    const dp = props[p.prop];
    const el = document.getElementById(p.id);
    const hint = document.getElementById(p.id + '-hint');
    const isLocal = dp?.source === 'local';
    el.value = isLocal && dp.value !== '-' ? dp.value : '';
    if (hint) {
      if (!isLocal && dp?.value && dp.value !== '-') {
        hint.textContent = 'inherited: ' + dp.value;
      } else if (!isLocal) {
        hint.textContent = 'not set';
      } else {
        hint.textContent = '';
      }
    }
  });
}

async function saveAutoSnapSchedule() {
  const body = {};
  body['com.sun:auto-snapshot'] = document.getElementById('autosnap-master').value.trim();
  _autoSnapPeriods.forEach(p => {
    body[p.prop] = document.getElementById(p.id).value.trim();
  });
  document.getElementById('autoSnapDialog').close();
  showOpLogRunning(`Auto-snapshot: ${_autoSnapDataset}`);
  try {
    const encodedName = _autoSnapDataset.split('/').map(encodeURIComponent).join('/');
    const result = await api('PUT', '/api/auto-snapshot/' + encodedName, body);
    showOpLog(`Auto-snapshot saved: ${_autoSnapDataset}`, result.tasks, null);
    toast('Auto-snapshot config saved', 'ok');
  } catch (err) {
    showOpLog(`Auto-snapshot save failed`, err.tasks, err.message);
  }
}

// ── Render: I/O Stats ─────────────────────────────────────────────────────────
function renderIOStat() {
  const wrap = document.getElementById('iostat-table-wrap');
  if (!state.iostat.length) {
    wrap.innerHTML = '<div class="loading">No I/O data.</div>';
    return;
  }
  const rows = state.iostat.map(s => `
    <tr>
      <td class="mono">${esc(s.pool)}</td>
      <td>${fmtNum(s.read_ops)}</td>
      <td>${fmtNum(s.write_ops)}</td>
      <td>${fmtBytes(s.read_bw)}/s</td>
      <td>${fmtBytes(s.write_bw)}/s</td>
    </tr>`).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Pool</th><th>Read IOPS</th><th>Write IOPS</th>
          <th>Read BW</th><th>Write BW</th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
}

function fmtNum(n) {
  if (!n) return '—';
  return n.toLocaleString(undefined, { maximumFractionDigits: 0 });
}

function fmtHours(h) {
  if (!h) return '—';
  if (h >= 8760) return (h / 8760).toFixed(1) + ' y';
  return h.toLocaleString() + ' h';
}

// ── Render: Disk Health (SMART) ───────────────────────────────────────────────
function renderSMART() {
  const wrap = document.getElementById('smart-wrap');
  const result = state.smart;
  if (!result || !result.available) {
    wrap.innerHTML = '<div class="loading">smartctl not installed — install smartmontools for disk health data.</div>';
    return;
  }
  if (!result.drives || !result.drives.length) {
    wrap.innerHTML = '<div class="loading">No drives found.</div>';
    return;
  }
  wrap.innerHTML = `<div class="smart-grid">${result.drives.map(renderDriveCard).join('')}</div>`;
}

function renderDriveCard(d) {
  const tempClass = d.temp_c > 60 ? 'temp-crit' : d.temp_c > 50 ? 'temp-warn' : '';
  const errCount = d.reallocated_sectors + d.pending_sectors + d.uncorrectable_errors
    + d.grown_defects + d.media_errors;
  const errStyle = errCount > 0 ? ' style="color:var(--red)"' : '';

  let errLine = '';
  if (d.protocol === 'SCSI') {
    errLine = `Grown defects: ${d.grown_defects}`;
  } else if (d.protocol === 'NVMe') {
    errLine = `Media errors: ${d.media_errors}`;
  } else {
    errLine = `Reallocated: ${d.reallocated_sectors} &nbsp;·&nbsp; Pending: ${d.pending_sectors} &nbsp;·&nbsp; Uncorrectable: ${d.uncorrectable_errors}`;
  }

  return `
    <div class="smart-card">
      <div class="smart-card-header">
        <span class="smart-device">${esc(d.device)}</span>
        ${d.protocol ? `<span class="proto-badge">${esc(d.protocol)}</span>` : ''}
        <span class="health-badge ${d.passed ? 'health-ONLINE' : 'health-FAULTED'}">${d.passed ? 'PASSED' : 'FAILED'}</span>
      </div>
      <div class="smart-model">${esc(d.model || '—')}</div>
      ${d.serial ? `<div class="smart-serial">S/N: ${esc(d.serial)}</div>` : ''}
      <div class="smart-stats">
        <div class="stat-item">
          <div class="stat-label">Capacity</div>
          <div class="stat-value">${d.capacity_bytes ? fmtBytes(d.capacity_bytes) : '—'}</div>
        </div>
        <div class="stat-item">
          <div class="stat-label">Temp</div>
          <div class="stat-value ${tempClass}">${d.temp_c ? d.temp_c + '°C' : '—'}</div>
        </div>
        <div class="stat-item">
          <div class="stat-label">Power-on</div>
          <div class="stat-value">${fmtHours(d.power_on_hours)}</div>
        </div>
      </div>
      <div class="smart-errors"${errStyle}>${errLine}</div>
    </div>`;
}

// ── Render: Datasets ──────────────────────────────────────────────────────────
let datasetFilter = '';
document.getElementById('dataset-filter').addEventListener('input', e => {
  datasetFilter = e.target.value.toLowerCase();
  renderDatasets();
});

// Returns true if name has any direct or indirect children in the full list.
function hasChildren(name, allDatasets) {
  return allDatasets.some(d => d.name.startsWith(name + '/'));
}

// Returns true if the row should be hidden because a collapsed ancestor contains it.
function isHiddenByCollapse(name) {
  const parts = name.split('/');
  for (let i = 1; i < parts.length; i++) {
    if (state.collapsedDatasets.has(parts.slice(0, i).join('/'))) return true;
  }
  return false;
}

function renderDatasets() {
  const wrap = document.getElementById('datasets-table-wrap');
  const all = state.datasets;

  // Apply text filter — when filtering, disable collapse logic for clarity.
  const filtering = datasetFilter.length > 0;
  let items = filtering
    ? all.filter(d => d.name.toLowerCase().includes(datasetFilter))
    : all.filter(d => !isHiddenByCollapse(d.name));

  if (!items.length) {
    wrap.innerHTML = '<div class="loading">No datasets found.</div>';
    return;
  }

  const rows = items.map(d => {
    const indent = `style="--depth:${d.depth}"`;
    const shortName = d.name.split('/').pop();
    const childCount = all.filter(c => c.name.startsWith(d.name + '/')).length;

    let toggle = '<span class="tree-spacer"></span>';
    if (!filtering && childCount > 0) {
      const collapsed = state.collapsedDatasets.has(d.name);
      const icon = collapsed ? '▶' : '▼';
      const title = collapsed ? `Expand (${childCount} hidden)` : 'Collapse';
      toggle = `<button class="tree-toggle" data-name="${esc(d.name)}" title="${title}">${icon}</button>`;
    }

    // Pool roots (depth 0) cannot be deleted here — use zpool destroy
    const canDelete = d.depth > 0;

    return `
      <tr>
        <td class="dataset-indent" ${indent}>${toggle}${typeBadge(d.depth === 0 ? 'pool' : d.type)} ${esc(shortName)}</td>
        <td>${fmtBytes(d.used)}</td>
        <td>${fmtBytes(d.avail)}</td>
        <td>${fmtBytes(d.refer)}</td>
        <td class="muted">${esc(d.compression)}</td>
        <td class="muted">${esc(d.compress_ratio)}</td>
        <td class="muted">${d.mountpoint !== 'none' ? esc(d.mountpoint) : '—'}</td>
        <td>
          <div class="row-actions">
            <button class="btn-edit" data-ds="${esc(d.name)}" data-type="${esc(d.type)}">Edit</button>
            ${d.type !== 'volume' ? `<button class="btn-acl btn-small${state.aclStatus[d.name] ? ' active' : ''}" data-ds="${esc(d.name)}" title="${state.aclStatus[d.name] ? 'ACL entries configured' : 'No ACL'}">ACL</button>` : ''}
            ${d.type === 'filesystem' && d.mountpoint !== 'none' && d.mountpoint !== '-' ? `<button class="btn-chown btn-small" data-ds="${esc(d.name)}">Chown</button>` : ''}
            ${d.type === 'filesystem' && d.mountpoint !== 'none' && d.mountpoint !== '-' ? `<button class="btn-nfs btn-small${d.sharenfs && d.sharenfs !== 'off' && d.sharenfs !== '-' ? ' active' : ''}" data-ds="${esc(d.name)}" title="${d.sharenfs && d.sharenfs !== 'off' && d.sharenfs !== '-' ? 'NFS shared: ' + esc(d.sharenfs) : 'Not shared'}">NFS</button>` : ''}
            ${d.type === 'filesystem' && d.mountpoint !== 'none' && d.mountpoint !== '-' ? (() => { const _sh = state.smbShares.find(s => s.path === d.mountpoint); return `<button class="btn-smb btn-small${_sh ? ' active' : ''}" data-ds="${esc(d.name)}" title="${_sh ? 'SMB shared: ' + esc(_sh.name) : 'Not shared'}">SMB</button>`; })() : ''}
            ${d.type === 'volume' ? (() => { const _it = state.iscsiTargets.find(t => t.zvol_name === d.name); return `<button class="btn-iscsi btn-small${_it ? ' active' : ''}" data-ds="${esc(d.name)}" title="${_it ? 'iSCSI target: ' + esc(_it.iqn) : 'Not exposed as iSCSI target'}">iSCSI</button>`; })() : ''}
            <button class="btn-autosnap btn-small${state.autoSnapshot[d.name]?.['com.sun:auto-snapshot']?.value === 'true' ? ' active' : ''}" data-ds="${esc(d.name)}" title="Auto-snapshot schedule">Snap</button>
            ${canDelete ? `<button class="btn-del" data-ds="${esc(d.name)}" data-type="${esc(d.type)}">Delete</button>` : ''}
          </div>
        </td>
      </tr>`;
  }).join('');

  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Name</th><th>Used</th><th>Avail</th>
          <th>Refer</th><th>Compress</th><th>Ratio</th><th>Mount</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;

  wrap.querySelectorAll('.tree-toggle').forEach(btn => {
    btn.addEventListener('click', () => {
      const name = btn.dataset.name;
      if (state.collapsedDatasets.has(name)) {
        state.collapsedDatasets.delete(name);
      } else {
        state.collapsedDatasets.add(name);
      }
      renderDatasets();
    });
  });

  wrap.querySelectorAll('.btn-edit[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openEditDatasetDialog(btn.dataset.ds, btn.dataset.type));
  });

  wrap.querySelectorAll('.btn-del[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openDeleteDatasetDialog(btn.dataset.ds, btn.dataset.type));
  });

  wrap.querySelectorAll('.btn-acl[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openACLDialog(btn.dataset.ds));
  });

  wrap.querySelectorAll('.btn-chown[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openChownDialog(btn.dataset.ds));
  });

  wrap.querySelectorAll('.btn-nfs[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openNFSDialog(btn.dataset.ds));
  });

  wrap.querySelectorAll('.btn-smb[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openSMBDialog(btn.dataset.ds));
  });

  wrap.querySelectorAll('.btn-iscsi[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openISCSIDialog(btn.dataset.ds));
  });

  wrap.querySelectorAll('.btn-autosnap[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openAutoSnapDialog(btn.dataset.ds));
  });
}

function typeBadge(type) {
  return `<span class="type-badge type-${esc(type)}">${esc(type)}</span>`;
}

// ── Render: Snapshots ─────────────────────────────────────────────────────────
let snapFilter = '';
document.getElementById('snap-filter').addEventListener('input', e => {
  snapFilter = e.target.value.toLowerCase();
  renderSnapshots();
});

function _updateMultiDeleteBtn() {
  const btn = document.getElementById('deleteMultiSnapBtn');
  if (!btn) return;
  const n = state.selectedSnaps.size;
  btn.style.display = n > 0 ? '' : 'none';
  btn.textContent = `Delete selected (${n})`;
}

function renderSnapshots() {
  const wrap = document.getElementById('snapshots-table-wrap');
  let items = state.snapshots;
  if (snapFilter) {
    items = items.filter(s => s.name.toLowerCase().includes(snapFilter));
  }
  if (!items.length) {
    wrap.innerHTML = '<div class="loading">No snapshots found.</div>';
    _updateMultiDeleteBtn();
    return;
  }
  const allVisible = items.map(s => s.name);
  const allChecked = allVisible.length > 0 && allVisible.every(n => state.selectedSnaps.has(n));
  const rows = items.map(s => {
    const checked = state.selectedSnaps.has(s.name) ? 'checked' : '';
    return `<tr>
      <td style="width:1.5rem"><input type="checkbox" class="snap-check" data-snap="${esc(s.name)}" ${checked}></td>
      <td class="mono">${esc(s.dataset)}</td>
      <td class="mono">${esc(s.snap_label)}</td>
      <td>${fmtBytes(s.used)}</td>
      <td>${fmtBytes(s.refer)}</td>
      <td class="muted">${fmtDate(s.creation)}</td>
      <td class="muted">${s.clones && s.clones !== '-' ? esc(s.clones) : '—'}</td>
      <td><button class="btn-del" data-snap="${esc(s.name)}">Delete</button></td>
    </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th style="width:1.5rem"><input type="checkbox" id="snapCheckAll" ${allChecked ? 'checked' : ''}></th>
          <th>Dataset</th><th>Snapshot</th><th>Used</th>
          <th>Refer</th><th>Created</th><th>Clones</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;

  wrap.querySelectorAll('.btn-del').forEach(btn => {
    btn.addEventListener('click', () => deleteSnapshot(btn.dataset.snap));
  });

  wrap.querySelectorAll('.snap-check').forEach(cb => {
    cb.addEventListener('change', () => {
      if (cb.checked) state.selectedSnaps.add(cb.dataset.snap);
      else state.selectedSnaps.delete(cb.dataset.snap);
      _updateMultiDeleteBtn();
      // sync select-all state
      const all = [...wrap.querySelectorAll('.snap-check')];
      document.getElementById('snapCheckAll').checked = all.length > 0 && all.every(c => c.checked);
    });
  });

  document.getElementById('snapCheckAll').addEventListener('change', e => {
    items.forEach(s => {
      if (e.target.checked) state.selectedSnaps.add(s.name);
      else state.selectedSnaps.delete(s.name);
    });
    renderSnapshots();
  });

  _updateMultiDeleteBtn();
}

const deleteSnapDialog = document.getElementById('deleteSnapDialog');
document.getElementById('deleteSnapCancelBtn').addEventListener('click', () => deleteSnapDialog.close());

let _deleteSnapName = '';
document.getElementById('deleteSnapConfirmBtn').addEventListener('click', async () => {
  const name = _deleteSnapName;
  deleteSnapDialog.close();
  showOpLogRunning(`Deleting snapshot…`);
  try {
    const result = await api('DELETE', '/api/snapshots/' + encodeURIComponent(name));
    storeSet('snapshots', state.snapshots.filter(s => s.name !== name));
    showOpLog(`Deleted snapshot: ${name}`, result.tasks, null);
  } catch (e) {
    showOpLog('Snapshot deletion failed', e.tasks, e.message);
  }
});

function deleteSnapshot(name) {
  _deleteSnapName = name;
  document.getElementById('deleteSnapDisplayName').textContent = name;
  deleteSnapDialog.showModal();
}

function openDeleteMultiSnapDialog() {
  const names = [...state.selectedSnaps];
  document.getElementById('deleteMultiSnapCount').textContent = names.length;
  document.getElementById('deleteMultiSnapPlural').textContent = names.length === 1 ? '' : 's';
  document.getElementById('deleteMultiSnapList').innerHTML = names.map(n => `<li>${esc(n)}</li>`).join('');
  document.getElementById('deleteMultiSnapDialog').showModal();
}

document.getElementById('deleteMultiSnapBtn').addEventListener('click', openDeleteMultiSnapDialog);
document.getElementById('deleteMultiSnapCancelBtn').addEventListener('click', () =>
  document.getElementById('deleteMultiSnapDialog').close());
document.getElementById('deleteMultiSnapConfirmBtn').addEventListener('click', async () => {
  const snapshots = [...state.selectedSnaps];
  document.getElementById('deleteMultiSnapDialog').close();
  showOpLogRunning(`Deleting ${snapshots.length} snapshot${snapshots.length === 1 ? '' : 's'}…`);
  try {
    const result = await api('POST', '/api/snapshots/delete-batch', { snapshots });
    state.selectedSnaps.clear();
    storeSet('snapshots', state.snapshots.filter(s => !snapshots.includes(s.name)));
    showOpLog(`Deleted ${snapshots.length} snapshot${snapshots.length === 1 ? '' : 's'}`, result.tasks, null);
  } catch (e) {
    showOpLog('Batch snapshot deletion failed', e.tasks, e.message);
  }
});

// ── New Snapshot dialog ───────────────────────────────────────────────────────
const dialog = document.getElementById('newSnapDialog');
document.getElementById('newSnapBtn').addEventListener('click', () => {
  // Pre-fill dataset if only one exists
  const datasets = state.datasets.filter(d => d.type === 'filesystem');
  if (datasets.length === 1) {
    document.getElementById('snap-dataset').value = datasets[0].name;
  }
  // Default label: current date
  const now = new Date();
  const label = now.toISOString().slice(0, 10) + '_manual';
  document.getElementById('snap-label').value = label;
  dialog.showModal();
});
document.getElementById('snapCancelBtn').addEventListener('click', () => dialog.close());

document.getElementById('newSnapForm').addEventListener('submit', async e => {
  e.preventDefault();
  const dataset = document.getElementById('snap-dataset').value.trim();
  const snapname = document.getElementById('snap-label').value.trim();
  const recursive = document.getElementById('snap-recursive').checked;
  dialog.close();
  showOpLogRunning(`Creating snapshot…`);
  try {
    const result = await api('POST', '/api/snapshots', { dataset, snapname, recursive });
    showOpLog(`Snapshot: ${dataset}@${snapname}`, result.tasks, null);
    const snaps = await api('GET', '/api/snapshots');
    storeSet('snapshots', snaps || []);
  } catch (e) {
    showOpLog('Snapshot creation failed', e.tasks, e.message);
  }
});

// ── New Dataset dialog ────────────────────────────────────────────────────────
const datasetDialog = document.getElementById('newDatasetDialog');
const dsType = document.getElementById('ds-type');

function updateDsTypeSections() {
  const isVol = dsType.value === 'volume';
  document.getElementById('ds-vol-section').style.display = isVol ? '' : 'none';
  document.getElementById('ds-fs-section').style.display  = isVol ? 'none' : '';
}

dsType.addEventListener('change', updateDsTypeSections);

document.getElementById('newDatasetBtn').addEventListener('click', () => {
  document.getElementById('newDatasetForm').reset();
  updateDsTypeSections();
  datasetDialog.showModal();
});
document.getElementById('datasetCancelBtn').addEventListener('click', () => datasetDialog.close());

document.getElementById('newDatasetForm').addEventListener('submit', async e => {
  e.preventDefault();
  const body = {
    name:    document.getElementById('ds-name').value.trim(),
    type:    document.getElementById('ds-type').value,
    volsize: document.getElementById('ds-volsize').value.trim(),
    sparse:  document.getElementById('ds-sparse').checked,
  };
  for (const p of (state.schema?.dataset_properties || [])) {
    if (!p.create) continue;
    const el = document.getElementById('ds-' + p.name);
    if (!el) continue;
    body[p.name] = p.input_type === 'text' ? el.value.trim() : el.value;
  }
  datasetDialog.close();
  showOpLogRunning('Creating dataset…');
  try {
    const result = await api('POST', '/api/datasets', body);
    showOpLog(`Dataset created: ${body.name}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    storeSet('datasets', datasets || []);
  } catch (e) {
    showOpLog('Dataset creation failed', e.tasks, e.message);
  }
});

// ── Delete Dataset dialog ─────────────────────────────────────────────────────
const deleteDatasetDialog   = document.getElementById('deleteDatasetDialog');
const deleteDatasetConfirm  = document.getElementById('deleteDatasetConfirmInput');
const deleteDatasetBtn      = document.getElementById('deleteDatasetConfirmBtn');
const deleteDatasetRecursive = document.getElementById('deleteDatasetRecursive');
const deleteDatasetChildWarn = document.getElementById('deleteDatasetChildWarning');
let _deleteDatasetTarget = '';

function openDeleteDatasetDialog(name, type) {
  _deleteDatasetTarget = name;
  document.getElementById('deleteDatasetDisplayName').textContent = name;
  document.getElementById('deleteDatasetConfirmHint').textContent = name;
  deleteDatasetConfirm.value = '';
  deleteDatasetRecursive.checked = false;
  deleteDatasetChildWarn.style.display = 'none';
  deleteDatasetBtn.disabled = true;

  // Volume-specific warning
  const volWarn = document.getElementById('deleteDatasetVolWarning');
  if (type === 'volume') {
    const snapCount = state.snapshots.filter(s => s.dataset === name).length;
    const snapNote = snapCount > 0
      ? ` It has ${snapCount} snapshot${snapCount > 1 ? 's' : ''} that will also be destroyed.`
      : '';
    volWarn.textContent = `\u26a0 This is a block device (zvol). Ensure it is not mounted or in use before deleting.${snapNote}`;
    volWarn.style.display = '';
  } else {
    volWarn.style.display = 'none';
  }

  deleteDatasetDialog.showModal();
  deleteDatasetConfirm.focus();
}

deleteDatasetConfirm.addEventListener('input', () => {
  deleteDatasetBtn.disabled = deleteDatasetConfirm.value !== _deleteDatasetTarget;
});

deleteDatasetRecursive.addEventListener('change', () => {
  deleteDatasetChildWarn.style.display = deleteDatasetRecursive.checked ? '' : 'none';
});

document.getElementById('deleteDatasetCancelBtn').addEventListener('click', () => {
  deleteDatasetDialog.close();
});

deleteDatasetBtn.addEventListener('click', async () => {
  if (deleteDatasetConfirm.value !== _deleteDatasetTarget) return;
  const name = _deleteDatasetTarget;
  const recursive = deleteDatasetRecursive.checked;
  deleteDatasetDialog.close();
  showOpLogRunning(`Deleting dataset…`);
  try {
    const encodedName = name.split('/').map(encodeURIComponent).join('/');
    const url = '/api/datasets/' + encodedName + (recursive ? '?recursive=true' : '');
    const result = await api('DELETE', url);
    showOpLog(`Deleted dataset: ${name}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    state.collapsedDatasets.delete(name);
    storeSet('datasets', datasets || []);
  } catch (e) {
    showOpLog(`Failed to delete ${name}`, e.tasks, e.message);
  }
});

// ── Edit Dataset dialog ───────────────────────────────────────────────────────
const editDatasetDialog = document.getElementById('editDatasetDialog');
let _editDatasetName = '';
let _editDatasetType = '';
let _editOriginalProps = {};  // prop → display value at open time

// Fields managed by the edit dialog — derived from schema at runtime.
function editSelectFields() {
  return (state.schema?.dataset_properties || [])
    .filter(p => p.editable && p.input_type === 'select')
    .map(p => p.name);
}
function editTextFields() {
  return (state.schema?.dataset_properties || [])
    .filter(p => p.editable && p.input_type === 'text')
    .map(p => p.name);
}

document.getElementById('editDatasetCancelBtn').addEventListener('click', () => editDatasetDialog.close());

async function openEditDatasetDialog(name, type) {
  _editDatasetName = name;
  _editDatasetType = type;
  document.getElementById('editDatasetTitle').textContent = `Edit: ${name}`;

  // Show/hide filesystem-only section.
  document.getElementById('edit-ds-fs-section').style.display = type === 'filesystem' ? '' : 'none';

  // Reset form before fetching.
  for (const f of [...editSelectFields(), ...editTextFields()]) {
    const el = document.getElementById('edit-ds-' + f);
    if (el) el.value = '';
  }
  _editOriginalProps = {};

  editDatasetDialog.showModal();

  try {
    const encodedName = name.split('/').map(encodeURIComponent).join('/');
    const props = await api('GET', '/api/dataset-props/' + encodedName);

    for (const [key, prop] of Object.entries(props)) {
      // Display value: locally-set properties show their value; inherited/default show "".
      const display = prop.source === 'local' ? prop.value : '';
      _editOriginalProps[key] = display;
      const el = document.getElementById('edit-ds-' + key);
      if (el) el.value = display;
    }
  } catch (e) {
    editDatasetDialog.close();
    toast('Failed to load dataset properties: ' + e.message, 'err');
  }
}

document.getElementById('editDatasetForm').addEventListener('submit', async e => {
  e.preventDefault();

  // Build body with only changed properties.
  const body = {};
  for (const f of editSelectFields()) {
    const el = document.getElementById('edit-ds-' + f);
    if (!el) continue;
    const val = el.value;
    if (val !== (_editOriginalProps[f] ?? '')) body[f] = val;
  }
  for (const f of editTextFields()) {
    const el = document.getElementById('edit-ds-' + f);
    if (!el) continue;
    const val = el.value.trim();
    if (val !== (_editOriginalProps[f] ?? '')) body[f] = val;
  }

  if (Object.keys(body).length === 0) {
    editDatasetDialog.close();
    toast('No changes to save', 'ok');
    return;
  }

  editDatasetDialog.close();
  showOpLogRunning('Updating properties…');
  try {
    const encodedName = _editDatasetName.split('/').map(encodeURIComponent).join('/');
    const result = await api('PATCH', '/api/datasets/' + encodedName, body);
    showOpLog(`Properties updated: ${_editDatasetName}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    storeSet('datasets', datasets || []);
  } catch (e) {
    showOpLog(`Failed to update ${_editDatasetName}`, e.tasks, e.message);
  }
});

// ── Protected account denylists (must match handlers.go) ─────────────────────
const PROTECTED_USERS  = new Set(['nobody', 'nfsnobody']);
const PROTECTED_GROUPS = new Set(['nogroup', 'nobody', 'nfsnobody']);

// ── Render: Users ─────────────────────────────────────────────────────────────
function renderUsers() {
  const btn = document.getElementById('toggleSystemUsersBtn');
  if (btn) btn.textContent = state.hideSystemUsers ? 'Show system' : 'Hide system';

  const wrap = document.getElementById('users-table-wrap');
  const users = state.hideSystemUsers
    ? state.users.filter(u => u.uid >= 1000 && !PROTECTED_USERS.has(u.username))
    : state.users;
  if (!users.length) {
    wrap.innerHTML = '<div class="loading">No users found.</div>';
    return;
  }
  const rows = users.map(u => {
    const isSystem = u.uid < 1000 || PROTECTED_USERS.has(u.username);
    return `
      <tr class="${isSystem ? 'row-muted' : ''}">
        <td class="mono">${esc(u.username)}</td>
        <td>${u.uid}</td>
        <td>${u.gid}</td>
        <td class="muted">${esc(u.shell)}</td>
        <td class="muted">${esc(u.home)}</td>
        <td>
          ${!isSystem ? `<button class="btn-edit" data-user="${esc(u.username)}">Edit</button>` : ''}
          ${!isSystem ? `<button class="btn-del" data-user="${esc(u.username)}">Delete</button>` : ''}
        </td>
      </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Username</th><th>UID</th><th>GID</th><th>Shell</th><th>Home</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.btn-edit[data-user]').forEach(btn => {
    btn.addEventListener('click', () => openEditUserDialog(btn.dataset.user));
  });
  wrap.querySelectorAll('.btn-del[data-user]').forEach(btn => {
    btn.addEventListener('click', () => openDeleteUserDialog(btn.dataset.user));
  });
}

// ── Render: Groups ────────────────────────────────────────────────────────────
function renderGroups() {
  const btn = document.getElementById('toggleSystemGroupsBtn');
  if (btn) btn.textContent = state.hideSystemGroups ? 'Show system' : 'Hide system';

  const wrap = document.getElementById('groups-table-wrap');
  const groups = state.hideSystemGroups
    ? state.groups.filter(g => g.gid >= 1000 && !PROTECTED_GROUPS.has(g.name))
    : state.groups;
  if (!groups.length) {
    wrap.innerHTML = '<div class="loading">No groups found.</div>';
    return;
  }
  const rows = groups.map(g => {
    const isSystem = g.gid < 1000 || PROTECTED_GROUPS.has(g.name);
    return `
      <tr class="${isSystem ? 'row-muted' : ''}">
        <td class="mono">${esc(g.name)}</td>
        <td>${g.gid}</td>
        <td class="muted">${(g.members || []).map(esc).join(', ') || '—'}</td>
        <td>
          ${!isSystem ? `<button class="btn-edit" data-group="${esc(g.name)}">Edit</button>` : ''}
          ${!isSystem ? `<button class="btn-del" data-group="${esc(g.name)}">Delete</button>` : ''}
        </td>
      </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Group</th><th>GID</th><th>Members</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.btn-edit[data-group]').forEach(btn => {
    btn.addEventListener('click', () => openEditGroupDialog(btn.dataset.group));
  });
  wrap.querySelectorAll('.btn-del[data-group]').forEach(btn => {
    btn.addEventListener('click', () => openDeleteGroupDialog(btn.dataset.group));
  });
}

// ── System user/group toggles ─────────────────────────────────────────────────
document.getElementById('toggleSystemUsersBtn').addEventListener('click', () => {
  state.hideSystemUsers = !state.hideSystemUsers;
  renderUsers();
});
document.getElementById('toggleSystemGroupsBtn').addEventListener('click', () => {
  state.hideSystemGroups = !state.hideSystemGroups;
  renderGroups();
});

// ── New User dialog ───────────────────────────────────────────────────────────
const newUserDialog = document.getElementById('newUserDialog');
document.getElementById('newUserBtn').addEventListener('click', () => {
  document.getElementById('newUserForm').reset();
  document.getElementById('user-create-group').checked = true;
  newUserDialog.showModal();
});
document.getElementById('userCancelBtn').addEventListener('click', () => newUserDialog.close());

document.getElementById('newUserForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username    = document.getElementById('user-name').value.trim();
  const shell       = document.getElementById('user-shell').value;
  const uid         = document.getElementById('user-uid').value.trim();
  const group       = document.getElementById('user-group').value.trim();
  const groups      = document.getElementById('user-groups').value.trim();
  const password    = document.getElementById('user-password').value;
  const createGroup = document.getElementById('user-create-group').checked;
  const smb_user    = document.getElementById('user-smb').checked;
  newUserDialog.close();
  showOpLogRunning('Creating user…');
  try {
    const result = await api('POST', '/api/users', { username, shell, uid, group, groups, password, create_group: createGroup, smb_user });
    showOpLog(`User created: ${username}`, result.tasks, null);
    const [users, smbData] = await Promise.all([
      api('GET', '/api/users'),
      api('GET', '/api/smb-users').catch(() => null),
    ]);
    storeBatch(() => {
      storeSet('users', users || []);
      storeSet('sambaAvailable', smbData?.available ?? false);
      storeSet('sambaUsers', smbData?.users || []);
    });
  } catch (err) {
    showOpLog('User creation failed', err.tasks, err.message);
  }
});

// ── Delete User dialog ────────────────────────────────────────────────────────
const deleteUserDialog = document.getElementById('deleteUserDialog');
const deleteUserConfirmInput = document.getElementById('deleteUserConfirmInput');
const deleteUserConfirmBtn   = document.getElementById('deleteUserConfirmBtn');
let _deleteUserTarget = '';

function openDeleteUserDialog(username) {
  _deleteUserTarget = username;
  document.getElementById('deleteUserDisplayName').textContent = username;
  document.getElementById('deleteUserConfirmHint').textContent = username;
  deleteUserConfirmInput.value = '';
  deleteUserConfirmBtn.disabled = true;
  deleteUserDialog.showModal();
  deleteUserConfirmInput.focus();
}

deleteUserConfirmInput.addEventListener('input', () => {
  deleteUserConfirmBtn.disabled = deleteUserConfirmInput.value !== _deleteUserTarget;
});

document.getElementById('deleteUserCancelBtn').addEventListener('click', () => deleteUserDialog.close());

deleteUserConfirmBtn.addEventListener('click', async () => {
  if (deleteUserConfirmInput.value !== _deleteUserTarget) return;
  const username = _deleteUserTarget;
  deleteUserDialog.close();
  showOpLogRunning('Deleting user…');
  try {
    const result = await api('DELETE', '/api/users/' + encodeURIComponent(username));
    showOpLog(`Deleted user: ${username}`, result.tasks, null);
    const users = await api('GET', '/api/users');
    storeSet('users', users || []);
  } catch (e) {
    showOpLog(`Failed to delete user: ${username}`, e.tasks, e.message);
  }
});

// ── New Group dialog ──────────────────────────────────────────────────────────
const newGroupDialog = document.getElementById('newGroupDialog');
document.getElementById('newGroupBtn').addEventListener('click', () => {
  document.getElementById('newGroupForm').reset();
  newGroupDialog.showModal();
});
document.getElementById('groupCancelBtn').addEventListener('click', () => newGroupDialog.close());

document.getElementById('newGroupForm').addEventListener('submit', async e => {
  e.preventDefault();
  const groupname = document.getElementById('group-name').value.trim();
  const gid       = document.getElementById('group-gid').value.trim();
  newGroupDialog.close();
  showOpLogRunning('Creating group…');
  try {
    const result = await api('POST', '/api/groups', { groupname, gid });
    showOpLog(`Group created: ${groupname}`, result.tasks, null);
    const groups = await api('GET', '/api/groups');
    storeSet('groups', groups || []);
  } catch (e) {
    showOpLog('Group creation failed', e.tasks, e.message);
  }
});

// ── Delete Group dialog ───────────────────────────────────────────────────────
const deleteGroupDialog = document.getElementById('deleteGroupDialog');
const deleteGroupConfirmInput = document.getElementById('deleteGroupConfirmInput');
const deleteGroupConfirmBtn   = document.getElementById('deleteGroupConfirmBtn');
let _deleteGroupTarget = '';

function openDeleteGroupDialog(groupname) {
  _deleteGroupTarget = groupname;
  document.getElementById('deleteGroupDisplayName').textContent = groupname;
  document.getElementById('deleteGroupConfirmHint').textContent = groupname;
  deleteGroupConfirmInput.value = '';
  deleteGroupConfirmBtn.disabled = true;
  deleteGroupDialog.showModal();
  deleteGroupConfirmInput.focus();
}

deleteGroupConfirmInput.addEventListener('input', () => {
  deleteGroupConfirmBtn.disabled = deleteGroupConfirmInput.value !== _deleteGroupTarget;
});

document.getElementById('deleteGroupCancelBtn').addEventListener('click', () => deleteGroupDialog.close());

deleteGroupConfirmBtn.addEventListener('click', async () => {
  if (deleteGroupConfirmInput.value !== _deleteGroupTarget) return;
  const groupname = _deleteGroupTarget;
  deleteGroupDialog.close();
  showOpLogRunning('Deleting group…');
  try {
    const result = await api('DELETE', '/api/groups/' + encodeURIComponent(groupname));
    showOpLog(`Deleted group: ${groupname}`, result.tasks, null);
    const groups = await api('GET', '/api/groups');
    storeSet('groups', groups || []);
  } catch (e) {
    showOpLog(`Failed to delete group: ${groupname}`, e.tasks, e.message);
  }
});

// ── Edit User dialog ──────────────────────────────────────────────────────────
const editUserDialog = document.getElementById('editUserDialog');
let _editUserTarget = '';

function openEditUserDialog(username) {
  const user = state.users.find(u => u.username === username);
  if (!user) return;
  _editUserTarget = username;
  document.getElementById('editUserDisplayName').textContent = username;

  const shellSel = document.getElementById('edit-user-shell');
  shellSel.value = user.shell;
  // If shell isn't in the list, add it as a temporary option
  if (shellSel.value !== user.shell) {
    const opt = document.createElement('option');
    opt.value = user.shell;
    opt.textContent = user.shell;
    shellSel.appendChild(opt);
    shellSel.value = user.shell;
  }

  // Primary group: find group whose gid matches user's gid
  const primaryGroup = state.groups.find(g => g.gid === user.gid);
  document.getElementById('edit-user-group').value = primaryGroup ? primaryGroup.name : '';

  // Supplementary groups: groups where user is in members, excluding primary
  const suppGroups = state.groups
    .filter(g => g.gid !== user.gid && (g.members || []).includes(username))
    .map(g => g.name)
    .join(',');
  document.getElementById('edit-user-groups').value = suppGroups;

  document.getElementById('edit-user-password').value = '';
  editUserDialog.showModal();
}

document.getElementById('editUserCancelBtn').addEventListener('click', () => editUserDialog.close());

document.getElementById('editUserForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username   = _editUserTarget;
  const shell      = document.getElementById('edit-user-shell').value;
  const group      = document.getElementById('edit-user-group').value.trim();
  const user_groups = document.getElementById('edit-user-groups').value.trim();
  const password   = document.getElementById('edit-user-password').value;
  editUserDialog.close();
  showOpLogRunning('Updating user…');
  try {
    const result = await api('PUT', '/api/users/' + encodeURIComponent(username), { shell, group, user_groups, password });
    showOpLog(`User updated: ${username}`, result.tasks, null);
    const [users, groups] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/groups')]);
    storeBatch(() => {
      storeSet('users', users || []);
      storeSet('groups', groups || []);
    });
  } catch (err) {
    showOpLog(`Failed to update user: ${username}`, err.tasks, err.message);
  }
});

// ── Edit Group dialog ─────────────────────────────────────────────────────────
const editGroupDialog = document.getElementById('editGroupDialog');
let _editGroupTarget = '';

function openEditGroupDialog(groupname) {
  const group = state.groups.find(g => g.name === groupname);
  if (!group) return;
  _editGroupTarget = groupname;
  document.getElementById('editGroupDisplayName').textContent = groupname;
  document.getElementById('edit-group-name').value = groupname;
  document.getElementById('edit-group-gid').value = '';
  document.getElementById('edit-group-members').value = (group.members || []).join(',');
  editGroupDialog.showModal();
}

document.getElementById('editGroupCancelBtn').addEventListener('click', () => editGroupDialog.close());

document.getElementById('editGroupForm').addEventListener('submit', async e => {
  e.preventDefault();
  const groupname = _editGroupTarget;
  const new_name  = document.getElementById('edit-group-name').value.trim();
  const gid       = document.getElementById('edit-group-gid').value.trim();
  const members   = document.getElementById('edit-group-members').value.trim();
  editGroupDialog.close();
  showOpLogRunning('Updating group…');
  try {
    const result = await api('PUT', '/api/groups/' + encodeURIComponent(groupname), { new_name, gid, members });
    showOpLog(`Group updated: ${result.groupname}`, result.tasks, null);
    const [users, groups] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/groups')]);
    storeBatch(() => {
      storeSet('users', users || []);
      storeSet('groups', groups || []);
    });
  } catch (err) {
    showOpLog(`Failed to update group: ${groupname}`, err.tasks, err.message);
  }
});

// ── Render: SMB Home Shares ───────────────────────────────────────────────────
function renderSMBHomes() {
  const wrap = document.getElementById('smb-homes-wrap');
  const cfg = state.smbHomes;
  if (!cfg.enabled) {
    wrap.innerHTML = `
      <div style="display:flex;align-items:center;gap:1rem;padding:0.5rem 0">
        <span class="muted">Home shares are disabled.</span>
        <button class="btn-primary btn-small" id="smbHomesEnableBtn">Enable</button>
      </div>`;
    document.getElementById('smbHomesEnableBtn').addEventListener('click', openSMBHomesDialog);
  } else {
    wrap.innerHTML = `
      <div class="table-wrap">
        <table>
          <thead><tr><th>Path</th><th>Browseable</th><th>Read Only</th><th>Create Mask</th><th>Dir Mask</th><th></th></tr></thead>
          <tbody>
            <tr>
              <td class="mono">${esc(cfg.path)}</td>
              <td>${esc(cfg.browseable)}</td>
              <td>${esc(cfg.read_only)}</td>
              <td class="mono">${esc(cfg.create_mask)}</td>
              <td class="mono">${esc(cfg.directory_mask)}</td>
              <td><button class="btn-secondary btn-small smb-homes-edit-btn">Edit</button></td>
            </tr>
          </tbody>
        </table>
      </div>`;
    wrap.querySelector('.smb-homes-edit-btn').addEventListener('click', openSMBHomesDialog);
  }
}

// ── SMB Home Shares dialog ───────────────────────────────────────────────────
const smbHomesDialog = document.getElementById('smbHomesDialog');

function openSMBHomesDialog() {
  const cfg = state.smbHomes;

  // Populate dataset picker
  const sel = document.getElementById('smb-homes-dataset');
  sel.innerHTML = '<option value="">— custom path —</option>';
  const fsList = (state.datasets || []).filter(d => d.type === 'filesystem' && d.mountpoint && d.mountpoint !== '-' && d.mountpoint !== 'none');
  fsList.forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.mountpoint;
    opt.textContent = d.name + ' (' + d.mountpoint + ')';
    sel.appendChild(opt);
  });

  // Pre-fill from current config
  const pathInput = document.getElementById('smb-homes-path');
  if (cfg.enabled && cfg.path) {
    pathInput.value = cfg.path;
    // Try to match dataset
    const base = cfg.path.replace(/\/%U$/, '');
    const matchOpt = Array.from(sel.options).find(o => o.value === base);
    if (matchOpt) sel.value = base;
  } else {
    pathInput.value = '';
    sel.value = '';
  }

  document.getElementById('smb-homes-browseable').value = cfg.browseable || 'no';
  document.getElementById('smb-homes-readonly').value = cfg.read_only || 'no';
  document.getElementById('smb-homes-create-mask').value = cfg.create_mask || '0644';
  document.getElementById('smb-homes-directory-mask').value = cfg.directory_mask || '0755';

  // Show/hide disable button
  const disableBtn = document.getElementById('smbHomesDisableBtn');
  const applyBtn = document.getElementById('smbHomesApplyBtn');
  disableBtn.style.display = cfg.enabled ? '' : 'none';
  applyBtn.textContent = cfg.enabled ? 'Apply' : 'Enable';

  smbHomesDialog.showModal();
}

// Dataset picker → auto-fill path
document.getElementById('smb-homes-dataset').addEventListener('change', function() {
  const pathInput = document.getElementById('smb-homes-path');
  if (this.value) {
    pathInput.value = this.value + '/%U';
  } else {
    pathInput.value = '';
  }
});

document.getElementById('smbHomesCancelBtn').addEventListener('click', () => smbHomesDialog.close());

document.getElementById('smbHomesApplyBtn').addEventListener('click', async () => {
  const path = document.getElementById('smb-homes-path').value.trim();
  if (!path) { toast('Path is required', 'err'); return; }
  const body = {
    path,
    browseable: document.getElementById('smb-homes-browseable').value,
    read_only: document.getElementById('smb-homes-readonly').value,
    create_mask: document.getElementById('smb-homes-create-mask').value.trim(),
    directory_mask: document.getElementById('smb-homes-directory-mask').value.trim(),
  };
  smbHomesDialog.close();
  showOpLogRunning('Configuring home shares…');
  try {
    const result = await api('POST', '/api/smb/homes', body);
    showOpLog('Home shares enabled', result.tasks, null);
    storeSet('smbHomes', result.config);
  } catch (err) {
    showOpLog('Failed to enable home shares', err.tasks, err.message);
  }
});

document.getElementById('smbHomesDisableBtn').addEventListener('click', async () => {
  smbHomesDialog.close();
  showOpLogRunning('Disabling home shares…');
  try {
    const result = await api('DELETE', '/api/smb/homes');
    showOpLog('Home shares disabled', result.tasks, null);
    storeSet('smbHomes', { enabled: false, path: '', browseable: 'no', read_only: 'no', create_mask: '0644', directory_mask: '0755' });
  } catch (err) {
    showOpLog('Failed to disable home shares', err.tasks, err.message);
  }
});

// ── Render: Time Machine Shares ───────────────────────────────────────────────
function renderTimeMachine() {
  const wrap = document.getElementById('tm-shares-wrap');
  const shares = state.timeMachineShares || [];
  if (!shares.length) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">No Time Machine shares configured.</div>';
    return;
  }
  const rows = shares.map(s => `
    <tr>
      <td class="mono">${esc(s.name)}</td>
      <td class="mono">${esc(s.path)}</td>
      <td>${s.max_size ? esc(s.max_size) : '<span class="muted">no limit</span>'}</td>
      <td>${s.valid_users ? esc(s.valid_users) : '<span class="muted">all</span>'}</td>
      <td><button class="btn-del btn-small tm-del-btn" data-name="${esc(s.name)}">Delete</button></td>
    </tr>`).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Share</th><th>Path</th><th>Max Size</th><th>Valid Users</th><th></th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.tm-del-btn').forEach(btn => {
    btn.addEventListener('click', () => deleteTimeMachineShare(btn.dataset.name));
  });
}

async function deleteTimeMachineShare(name) {
  if (!confirm(`Delete Time Machine share "${name}"?`)) return;
  showOpLogRunning('Removing Time Machine share…');
  try {
    const result = await api('DELETE', '/api/smb/timemachine/' + encodeURIComponent(name));
    showOpLog(`Time Machine share deleted: ${name}`, result.tasks, null);
    const shares = await api('GET', '/api/smb/timemachine').catch(() => []);
    storeSet('timeMachineShares', shares || []);
  } catch (err) {
    showOpLog(`Failed to delete Time Machine share: ${name}`, err.tasks, err.message);
  }
}

// ── Time Machine dialog ──────────────────────────────────────────────────────
const timeMachineDialog = document.getElementById('timeMachineDialog');

function openTimeMachineDialog() {
  document.getElementById('tm-sharename').value = '';
  document.getElementById('tm-path').value = '';
  document.getElementById('tm-max-size').value = '';
  document.getElementById('tm-valid-users').value = '';

  // Populate dataset picker
  const sel = document.getElementById('tm-dataset');
  sel.innerHTML = '<option value="">— custom path —</option>';
  const fsList = (state.datasets || []).filter(d => d.type === 'filesystem' && d.mountpoint && d.mountpoint !== '-' && d.mountpoint !== 'none');
  fsList.forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.mountpoint;
    opt.textContent = d.name + ' (' + d.mountpoint + ')';
    sel.appendChild(opt);
  });
  sel.value = '';

  timeMachineDialog.showModal();
  document.getElementById('tm-sharename').focus();
}

document.getElementById('tmAddBtn').addEventListener('click', openTimeMachineDialog);

document.getElementById('tm-dataset').addEventListener('change', function() {
  const pathInput = document.getElementById('tm-path');
  if (this.value) {
    pathInput.value = this.value;
  } else {
    pathInput.value = '';
  }
});

document.getElementById('tmCancelBtn').addEventListener('click', () => timeMachineDialog.close());

document.getElementById('tmCreateBtn').addEventListener('click', async () => {
  const sharename = document.getElementById('tm-sharename').value.trim();
  const path = document.getElementById('tm-path').value.trim();
  if (!sharename) { toast('Share name is required', 'err'); return; }
  if (!path) { toast('Path is required', 'err'); return; }
  const body = {
    sharename,
    path,
    max_size: document.getElementById('tm-max-size').value.trim(),
    valid_users: document.getElementById('tm-valid-users').value.trim(),
  };
  timeMachineDialog.close();
  showOpLogRunning('Creating Time Machine share…');
  try {
    const result = await api('POST', '/api/smb/timemachine', body);
    showOpLog('Time Machine share created', result.tasks, null);
    storeSet('timeMachineShares', result.shares || []);
  } catch (err) {
    showOpLog('Failed to create Time Machine share', err.tasks, err.message);
  }
});

// ── Render: SMB Users ─────────────────────────────────────────────────────────
function renderSambaUsers() {
  const wrap = document.getElementById('smb-users-wrap');
  if (!state.sambaAvailable) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">Samba (pdbedit) not available on this system.</div>';
    return;
  }
  const smbSet = new Set(state.sambaUsers);
  const regularUsers = state.users.filter(u => u.uid >= 1000 && !PROTECTED_USERS.has(u.username));
  if (!regularUsers.length) {
    wrap.innerHTML = '<div class="loading">No regular users found.</div>';
    return;
  }
  const rows = regularUsers.map(u => {
    const registered = smbSet.has(u.username);
    const statusHtml = registered
      ? '<span class="smb-status smb-yes">● registered</span>'
      : '<span class="smb-status smb-no">○ not registered</span>';
    const actionBtn = registered
      ? `<button class="btn-del smb-remove-btn" data-user="${esc(u.username)}">Remove from SMB</button>`
      : `<button class="btn-secondary smb-add-btn" data-user="${esc(u.username)}">Add to SMB</button>`;
    return `
      <tr>
        <td class="mono">${esc(u.username)}</td>
        <td>${u.uid}</td>
        <td>${statusHtml}</td>
        <td>${actionBtn}</td>
      </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Username</th><th>UID</th><th>SMB Status</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.smb-add-btn').forEach(btn => {
    btn.addEventListener('click', () => openAddSmbUserDialog(btn.dataset.user));
  });
  wrap.querySelectorAll('.smb-remove-btn').forEach(btn => {
    btn.addEventListener('click', () => removeSmbUser(btn.dataset.user));
  });
}

// ── Add SMB User dialog ───────────────────────────────────────────────────────
const addSmbUserDialog = document.getElementById('addSmbUserDialog');
let _addSmbUserTarget = '';

function openAddSmbUserDialog(username) {
  _addSmbUserTarget = username;
  document.getElementById('addSmbUserDisplayName').textContent = username;
  document.getElementById('smb-user-password').value = '';
  addSmbUserDialog.showModal();
  document.getElementById('smb-user-password').focus();
}

document.getElementById('addSmbUserCancelBtn').addEventListener('click', () => addSmbUserDialog.close());

document.getElementById('addSmbUserForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username = _addSmbUserTarget;
  const password = document.getElementById('smb-user-password').value;
  addSmbUserDialog.close();
  showOpLogRunning('Adding SMB user…');
  try {
    const result = await api('POST', '/api/smb-users/' + encodeURIComponent(username), { password });
    showOpLog(`SMB access added: ${username}`, result.tasks, null);
    const smbData = await api('GET', '/api/smb-users').catch(() => null);
    storeBatch(() => {
      storeSet('sambaAvailable', smbData?.available ?? false);
      storeSet('sambaUsers', smbData?.users || []);
    });
  } catch (err) {
    showOpLog(`Failed to add SMB access: ${username}`, err.tasks, err.message);
  }
});

// ── Remove SMB User dialog ────────────────────────────────────────────────────
const removeSmbUserDialog = document.getElementById('removeSmbUserDialog');
let _removeSmbUserTarget = '';

function removeSmbUser(username) {
  _removeSmbUserTarget = username;
  document.getElementById('removeSmbUserDisplayName').textContent = username;
  removeSmbUserDialog.showModal();
}

document.getElementById('removeSmbUserCancelBtn').addEventListener('click', () => removeSmbUserDialog.close());

document.getElementById('removeSmbUserConfirmBtn').addEventListener('click', async () => {
  const username = _removeSmbUserTarget;
  removeSmbUserDialog.close();
  showOpLogRunning('Removing SMB user…');
  try {
    const result = await api('DELETE', '/api/smb-users/' + encodeURIComponent(username));
    showOpLog(`SMB access removed: ${username}`, result.tasks, null);
    const smbData = await api('GET', '/api/smb-users').catch(() => null);
    storeBatch(() => {
      storeSet('sambaAvailable', smbData?.available ?? false);
      storeSet('sambaUsers', smbData?.users || []);
    });
  } catch (err) {
    showOpLog(`Failed to remove SMB access: ${username}`, err.tasks, err.message);
  }
});

// ── Configure Samba dialog ────────────────────────────────────────────────────
const configureSambaDialog = document.getElementById('configureSambaDialog');
document.getElementById('smbConfigPamBtn').addEventListener('click', () => configureSambaDialog.showModal());
document.getElementById('configureSambaCancelBtn').addEventListener('click', () => configureSambaDialog.close());

document.getElementById('configureSambaConfirmBtn').addEventListener('click', async () => {
  configureSambaDialog.close();
  showOpLogRunning('Configuring Samba…');
  try {
    const result = await api('POST', '/api/smb-config/pam');
    showOpLog('Samba configured', result.tasks, null);
  } catch (err) {
    showOpLog('Failed to configure Samba', err.tasks, err.message);
  }
});

// ── ACL dialog ────────────────────────────────────────────────────────────────
const aclDialog = document.getElementById('aclDialog');
document.getElementById('aclDialogClose').addEventListener('click', () => aclDialog.close());

async function openACLDialog(dataset) {
  state.aclDataset = dataset;
  state.aclData = null;
  document.getElementById('aclDialogTitle').textContent = `ACL — ${dataset}`;
  document.getElementById('aclDialogMeta').innerHTML = '<span class="muted">Loading…</span>';
  document.getElementById('aclDialogEntries').innerHTML = '';
  document.getElementById('aclDialogAddForm').innerHTML = '';
  aclDialog.showModal();
  try {
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
  } catch (err) {
    document.getElementById('aclDialogMeta').innerHTML =
      `<p class="op-error">Failed to load ACL: ${esc(err.message)}</p>`;
  }
}

function renderACLDialog() {
  const d = state.aclData;
  if (!d) return;

  const typeBadgeACL = t => `<span class="type-badge type-${esc(t)}">${esc(t)}</span>`;
  const disableBtn = d.acl_type !== 'off'
    ? `<button class="btn-danger btn-small" id="aclDisable">Disable ACLs</button>`
    : '';
  document.getElementById('aclDialogMeta').innerHTML =
    `<p class="acl-meta">Mountpoint: <code>${esc(d.mountpoint || '—')}</code> &nbsp; Type: ${typeBadgeACL(d.acl_type || 'off')} ${disableBtn}</p>`;
  if (disableBtn) {
    document.getElementById('aclDisable').addEventListener('click', () => disableACLs(d.dataset));
  }

  if (d.acl_type === 'off') {
    document.getElementById('aclDialogEntries').innerHTML = `
      <div class="acl-off">
        <p class="muted">ACLs are not enabled on this dataset.</p>
        <div class="row-actions">
          <button class="btn-primary" id="aclEnablePosix">Enable POSIX ACLs</button>
          <button class="btn-secondary" id="aclEnableNfs4">Enable NFSv4 ACLs</button>
        </div>
      </div>`;
    document.getElementById('aclDialogAddForm').innerHTML = '';
    document.getElementById('aclEnablePosix').addEventListener('click', () => enableACLs(d.dataset, 'posix'));
    document.getElementById('aclEnableNfs4').addEventListener('click', () => enableACLs(d.dataset, 'nfsv4'));
    return;
  }

  if (d.acl_type === 'posix') {
    renderPOSIXACLEntries(d);
    renderPOSIXAddForm(d);
  } else if (d.acl_type === 'nfsv4') {
    renderNFSv4ACLEntries(d);
    renderNFSv4AddForm(d);
  }
}

function renderPOSIXACLEntries(d) {
  const entries = d.entries || [];
  if (!entries.length) {
    document.getElementById('aclDialogEntries').innerHTML = '<p class="muted">No ACL entries.</p>';
    return;
  }
  const mandatoryTags = new Set(['user', 'group', 'other']);
  const rows = entries.map(e => {
    const removal = (e.default ? 'default:' : '') + e.tag + (e.qualifier ? ':' + e.qualifier : '');
    const perms = e.perms || '---';
    const mandatory = !e.default && !e.qualifier && mandatoryTags.has(e.tag);
    const delBtn = mandatory ? '' : `<button class="btn-del btn-small" data-entry="${esc(removal)}">✕</button>`;
    return `<tr>
      <td>${e.default ? '<span class="type-badge type-volume">default</span> ' : ''}${esc(e.tag)}</td>
      <td>${esc(e.qualifier) || '<span class="muted">—</span>'}</td>
      <td class="acl-perms"><code>${esc(perms.replace(/-/g,'·'))}</code></td>
      <td>${delBtn}</td>
    </tr>`;
  }).join('');

  document.getElementById('aclDialogEntries').innerHTML = `
    <table class="acl-table">
      <thead><tr><th>Tag</th><th>Qualifier</th><th>Perms</th><th></th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`;

  document.getElementById('aclDialogEntries').querySelectorAll('.btn-del[data-entry]').forEach(btn => {
    btn.addEventListener('click', () => removeACLEntry(btn.dataset.entry, false));
  });
}

function renderNFSv4ACLEntries(d) {
  const entries = d.entries || [];
  if (!entries.length) {
    document.getElementById('aclDialogEntries').innerHTML = '<p class="muted">No ACL entries.</p>';
    return;
  }
  const typeLabel = { A: 'Allow', D: 'Deny', U: 'Audit', L: 'Alarm' };
  const rows = entries.map(e => {
    const ace = `${e.tag}:${e.flags}:${e.qualifier}:${e.perms}`;
    return `<tr>
      <td>${esc(typeLabel[e.tag] || e.tag)}</td>
      <td class="muted">${esc(e.flags) || '—'}</td>
      <td>${esc(e.qualifier)}</td>
      <td><code class="acl-perms">${esc(e.perms)}</code></td>
      <td><button class="btn-del btn-small" data-entry="${esc(ace)}">✕</button></td>
    </tr>`;
  }).join('');

  document.getElementById('aclDialogEntries').innerHTML = `
    <table class="acl-table">
      <thead><tr><th>Type</th><th>Flags</th><th>Principal</th><th>Perms</th><th></th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`;

  document.getElementById('aclDialogEntries').querySelectorAll('.btn-del[data-entry]').forEach(btn => {
    btn.addEventListener('click', () => removeACLEntry(btn.dataset.entry, false));
  });
}

function renderPOSIXAddForm(_d) {
  const userList = state.users.map(u => `<option value="${esc(u.username)}">`).join('');
  const groupList = state.groups.map(g => `<option value="${esc(g.name)}">`).join('');

  document.getElementById('aclDialogAddForm').innerHTML = `
    <fieldset class="form-section acl-add-form">
      <legend>Add Entry</legend>
      <datalist id="aclUserList">${userList}</datalist>
      <datalist id="aclGroupList">${groupList}</datalist>
      <div class="form-grid">
        <label>Tag
          <select id="aclTag">
            <option value="user">user</option>
            <option value="group">group</option>
            <option value="mask">mask</option>
            <option value="other">other</option>
          </select>
        </label>
        <label id="aclQualifierLabel">Qualifier
          <input type="text" id="aclQualifier" list="aclUserList" placeholder="username or groupname" autocomplete="off">
        </label>
      </div>
      <div class="acl-perms-row">
        <label class="checkbox-label"><input type="checkbox" id="aclPermR" checked> read (r)</label>
        <label class="checkbox-label"><input type="checkbox" id="aclPermW"> write (w)</label>
        <label class="checkbox-label"><input type="checkbox" id="aclPermX"> execute (x)</label>
        <label class="checkbox-label"><input type="checkbox" id="aclDefault"> default</label>
        <label class="checkbox-label"><input type="checkbox" id="aclRecursive"> recursive</label>
      </div>
      <div class="dialog-actions" style="margin-top:0.5rem;padding-top:0">
        <button type="button" class="btn-primary" id="aclAddBtn">Add Entry</button>
      </div>
    </fieldset>`;

  const tagSel = document.getElementById('aclTag');
  const qualLabel = document.getElementById('aclQualifierLabel');
  const qualInput = document.getElementById('aclQualifier');
  tagSel.addEventListener('change', () => {
    const t = tagSel.value;
    const hasQualifier = t === 'user' || t === 'group';
    qualLabel.style.display = hasQualifier ? '' : 'none';
    if (t === 'group') {
      qualInput.setAttribute('list', 'aclGroupList');
    } else {
      qualInput.setAttribute('list', 'aclUserList');
    }
  });

  document.getElementById('aclAddBtn').addEventListener('click', async () => {
    const tag = tagSel.value;
    const qualifier = qualInput.value.trim();
    const r = document.getElementById('aclPermR').checked ? 'r' : '-';
    const w = document.getElementById('aclPermW').checked ? 'w' : '-';
    const x = document.getElementById('aclPermX').checked ? 'x' : '-';
    const isDefault = document.getElementById('aclDefault').checked;
    const recursive = document.getElementById('aclRecursive').checked;

    let spec = tag;
    if (tag === 'user' || tag === 'group') spec += ':' + qualifier;
    else spec += ':';
    spec += ':' + r + w + x;
    if (isDefault) spec = 'default:' + spec;

    await addACLEntry(spec, recursive);
  });
}

function renderNFSv4AddForm(_d) {
  document.getElementById('aclDialogAddForm').innerHTML = `
    <fieldset class="form-section acl-add-form">
      <legend>Add Entry</legend>
      <div class="form-grid">
        <label>Type
          <select id="aclNfs4Type">
            <option value="A">Allow (A)</option>
            <option value="D">Deny (D)</option>
          </select>
        </label>
        <label>Principal
          <input type="text" id="aclNfs4Principal" placeholder="OWNER@, GROUP@, EVERYONE@, user@domain" list="aclNfs4Principals" autocomplete="off">
          <datalist id="aclNfs4Principals">
            <option value="OWNER@">
            <option value="GROUP@">
            <option value="EVERYONE@">
            ${state.users.map(u => `<option value="${esc(u.username)}@localdomain">`).join('')}
          </datalist>
        </label>
      </div>
      <div class="acl-perms-row">
        <span class="field-note">Flags:</span>
        <label class="checkbox-label"><input type="checkbox" id="nfs4FlagF"> file-inherit (f)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4FlagD"> dir-inherit (d)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4FlagI"> inherit-only (i)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4FlagN"> no-propagate (n)</label>
      </div>
      <div class="acl-perms-row">
        <span class="field-note">Perms:</span>
        <label class="checkbox-label"><input type="checkbox" id="nfs4R" checked> read (r)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4W"> write (w)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4X"> execute (x)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4A"> append (a)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4Del"> delete (d)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4DelC"> del-child (D)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4C"> read-ACL (c)</label>
        <label class="checkbox-label"><input type="checkbox" id="nfs4BigC"> write-ACL (C)</label>
      </div>
      <div class="dialog-actions" style="margin-top:0.5rem;padding-top:0">
        <button type="button" class="btn-primary" id="aclNfs4AddBtn">Add Entry</button>
      </div>
    </fieldset>`;

  document.getElementById('aclNfs4AddBtn').addEventListener('click', async () => {
    const type = document.getElementById('aclNfs4Type').value;
    const principal = document.getElementById('aclNfs4Principal').value.trim();
    if (!principal) { toast('Principal is required', 'error'); return; }

    const flags = [
      document.getElementById('nfs4FlagF').checked ? 'f' : '',
      document.getElementById('nfs4FlagD').checked ? 'd' : '',
      document.getElementById('nfs4FlagI').checked ? 'i' : '',
      document.getElementById('nfs4FlagN').checked ? 'n' : '',
    ].join('');

    const perms = [
      document.getElementById('nfs4R').checked    ? 'r' : '',
      document.getElementById('nfs4W').checked    ? 'w' : '',
      document.getElementById('nfs4X').checked    ? 'x' : '',
      document.getElementById('nfs4A').checked    ? 'a' : '',
      document.getElementById('nfs4Del').checked  ? 'd' : '',
      document.getElementById('nfs4DelC').checked ? 'D' : '',
      document.getElementById('nfs4C').checked    ? 'c' : '',
      document.getElementById('nfs4BigC').checked ? 'C' : '',
    ].join('');

    if (!perms) { toast('Select at least one permission', 'error'); return; }

    const ace = `${type}:${flags}:${principal}:${perms}`;
    await addACLEntry(ace, false);
  });
}

async function refreshACLStatus() {
  try {
    storeSet('aclStatus', await api('GET', '/api/acl-status') || {});
  } catch (_) { /* best-effort */ }
}

async function addACLEntry(ace, recursive) {
  const dataset = state.aclDataset;
  showOpLogRunning('Applying ACL entry…');
  try {
    const res = await api('POST', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { ace, recursive });
    showOpLog(`ACL entry added — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
    refreshACLStatus();
  } catch (err) {
    showOpLog(`Failed to add ACL entry`, err.tasks, err.message);
  }
}

async function removeACLEntry(entry, recursive) {
  const dataset = state.aclDataset;
  showOpLogRunning('Removing ACL entry…');
  try {
    const qs = `entry=${encodeURIComponent(entry)}${recursive ? '&recursive=true' : ''}`;
    const res = await api('DELETE', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}?${qs}`);
    showOpLog(`ACL entry removed — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
    refreshACLStatus();
  } catch (err) {
    showOpLog(`Failed to remove ACL entry`, err.tasks, err.message);
  }
}

async function enableACLs(dataset, acltype) {
  showOpLogRunning('Enabling ACLs…');
  try {
    const res = await api('PATCH', `/api/datasets/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { acltype });
    showOpLog(`Enabled ${acltype} ACLs — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
    refreshACLStatus();
  } catch (err) {
    showOpLog(`Failed to enable ACLs`, err.tasks, err.message);
  }
}

const disableACLDialog = document.getElementById('disableACLDialog');
document.getElementById('disableACLCancelBtn').addEventListener('click', () => disableACLDialog.close());
document.getElementById('disableACLConfirmBtn').addEventListener('click', async () => {
  const dataset = state.aclDataset;
  disableACLDialog.close();
  showOpLogRunning('Disabling ACLs…');
  try {
    const res = await api('PATCH', `/api/datasets/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { acltype: 'off' });
    showOpLog(`Disabled ACLs — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
    refreshACLStatus();
  } catch (err) {
    showOpLog(`Failed to disable ACLs`, err.tasks, err.message);
  }
});

function disableACLs(dataset) {
  document.getElementById('disableACLDatasetName').textContent = dataset;
  disableACLDialog.showModal();
}

// ── XSS helper ────────────────────────────────────────────────────────────────
function esc(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ── Chown dialog ──────────────────────────────────────────────────────────────
const chownDialog = document.getElementById('chownDialog');
document.getElementById('chownCancelBtn').addEventListener('click', () => chownDialog.close());

let _chownDataset = '';

async function openChownDialog(dataset) {
  _chownDataset = dataset;
  document.getElementById('chownDatasetLabel').textContent = dataset;
  document.getElementById('chown-recursive').checked = false;

  // Populate owner/group selects from cached state
  const ownerSel = document.getElementById('chown-owner');
  const groupSel = document.getElementById('chown-group');
  ownerSel.innerHTML = state.users.map(u => `<option value="${esc(u.username)}">${esc(u.username)}</option>`).join('');
  groupSel.innerHTML = state.groups.map(g => `<option value="${esc(g.name)}">${esc(g.name)}</option>`).join('');

  chownDialog.showModal();

  // Pre-select current owner/group
  try {
    const info = await api('GET', `/api/chown/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    if (info.owner) ownerSel.value = info.owner;
    if (info.group) groupSel.value = info.group;
  } catch (err) {
    toast(`Could not read ownership: ${err.message}`, 'error');
  }
}

document.getElementById('chownForm').addEventListener('submit', async e => {
  e.preventDefault();
  const dataset = _chownDataset;
  const owner = document.getElementById('chown-owner').value;
  const group = document.getElementById('chown-group').value;
  const recursive = document.getElementById('chown-recursive').checked;
  chownDialog.close();
  showOpLogRunning('Changing ownership…');
  try {
    const result = await api('POST', `/api/chown/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { owner, group, recursive });
    showOpLog(`Ownership changed: ${dataset}`, result.tasks, null);
  } catch (err) {
    showOpLog(`Failed to change ownership of ${dataset}`, err.tasks, err.message);
  }
});

// ── NFS share dialog (sharenfs property) ─────────────────────────────────────
const nfsDialog = document.getElementById('nfsDialog');
document.getElementById('nfsDialogClose').addEventListener('click', () => nfsDialog.close());

let _nfsDataset = '';

async function openNFSDialog(dataset) {
  _nfsDataset = dataset;
  document.getElementById('nfsDialogTitle').textContent = 'NFS Share — ' + dataset;
  document.getElementById('nfsDialogPath').textContent = '';
  document.getElementById('nfs-clients').value = '';
  document.getElementById('nfsDialogEntries').innerHTML = '<span class="muted">Loading…</span>';
  nfsDialog.showModal();
  try {
    const props = await api('GET', '/api/dataset-props/' + encodeURIComponent(dataset).replace(/%2F/g, '/'));
    const sharenfs = props.sharenfs?.value ?? 'off';
    const src = props.sharenfs?.source ?? '';
    const entriesEl = document.getElementById('nfsDialogEntries');
    if (sharenfs && sharenfs !== 'off' && sharenfs !== '-') {
      entriesEl.innerHTML = `
        <div class="acl-entry" style="display:flex;align-items:center;gap:0.5rem">
          <code style="flex:1">${esc(sharenfs)}</code>
          <span class="muted" style="font-size:0.8rem">${esc(src)}</span>
        </div>`;
      document.getElementById('nfs-clients').value = sharenfs === 'on' ? '' : sharenfs;
    } else {
      entriesEl.innerHTML = '<p class="muted">Not currently shared via NFS.</p>';
    }
  } catch (e) {
    document.getElementById('nfsDialogEntries').innerHTML = `<p class="op-error">${esc(e.message)}</p>`;
  }
}

document.getElementById('nfsAddBtn').addEventListener('click', async () => {
  const options = document.getElementById('nfs-clients').value.trim() || 'on';
  const dataset = _nfsDataset;
  nfsDialog.close();
  showOpLogRunning('Enabling NFS share…');
  try {
    const result = await api('PATCH', '/api/datasets/' + encodeURIComponent(dataset).replace(/%2F/g, '/'),
      { sharenfs: options });
    showOpLog('NFS share enabled: ' + dataset, result.tasks, null);
  } catch (e) {
    showOpLog('Failed to set sharenfs', e.tasks, e.message);
  }
});

document.getElementById('nfsDisableBtn').addEventListener('click', async () => {
  const dataset = _nfsDataset;
  nfsDialog.close();
  showOpLogRunning('Disabling NFS share…');
  try {
    const result = await api('PATCH', '/api/datasets/' + encodeURIComponent(dataset).replace(/%2F/g, '/'),
      { sharenfs: 'off' });
    showOpLog('NFS share disabled: ' + dataset, result.tasks, null);
  } catch (e) {
    showOpLog('Failed to disable sharenfs', e.tasks, e.message);
  }
});




// ── SMB share dialog (net usershare) ─────────────────────────────────────────
const smbDialog = document.getElementById('smbDialog');
document.getElementById('smbDialogClose').addEventListener('click', () => smbDialog.close());

let _smbDataset = '';
let _smbCurrentSharename = ''; // share name of the active usershare (if any)

function _smbUNCPreview(sharename, dataset) {
  const ds = dataset || _smbDataset;
  const host = state.sysinfo?.hostname || 'server';
  const derived = ds.replace(/\//g, '-');
  const name = sharename.trim() || derived;
  document.getElementById('smb-unc-preview').textContent = `Accessible as \\\\${host}\\${name}`;
}

async function openSMBDialog(dataset) {
  _smbDataset = dataset;
  _smbCurrentSharename = '';
  document.getElementById('smbDialogTitle').textContent = 'SMB Share — ' + dataset;
  document.getElementById('smb-sharename').value = '';
  document.getElementById('smbDialogEntries').innerHTML = '<span class="muted">Loading…</span>';
  _smbUNCPreview('', dataset);
  smbDialog.showModal();
  try {
    // Get dataset mountpoint, then match against live usershare list
    const [props, shares] = await Promise.all([
      api('GET', '/api/dataset-props/' + encodeURIComponent(dataset).replace(/%2F/g, '/')),
      api('GET', '/api/smb-shares'),
    ]);
    storeSet('smbShares', shares || []);
    const mountpoint = props.mountpoint?.value ?? '';
    const existing = state.smbShares.find(s => s.path === mountpoint);
    const entriesEl = document.getElementById('smbDialogEntries');
    if (existing) {
      _smbCurrentSharename = existing.name;
      document.getElementById('smb-sharename').value = existing.name;
      _smbUNCPreview(existing.name, dataset);
      entriesEl.innerHTML = `
        <div class="acl-entry" style="display:flex;align-items:center;gap:0.5rem">
          <span class="field-note">Currently shared as</span>
          <code style="flex:1">${esc(existing.name)}</code>
          <span class="muted" style="font-size:0.8rem">${esc(mountpoint)}</span>
        </div>`;
    } else {
      entriesEl.innerHTML = '<p class="muted">Not currently shared via SMB.</p>';
    }
  } catch (e) {
    document.getElementById('smbDialogEntries').innerHTML = `<p class="op-error">${esc(e.message)}</p>`;
  }
}

document.getElementById('smb-sharename').addEventListener('input', e => {
  _smbUNCPreview(e.target.value, _smbDataset);
});

async function _refreshSMBShares() {
  const shares = await api('GET', '/api/smb-shares').catch(() => []);
  storeSet('smbShares', shares || []);
}

document.getElementById('smbAddBtn').addEventListener('click', async () => {
  const sharename = document.getElementById('smb-sharename').value.trim() || _smbDataset.replace(/\//g, '-');
  const dataset = _smbDataset;
  smbDialog.close();
  showOpLogRunning('Enabling SMB share…');
  try {
    const result = await api('POST', '/api/smb-share/' + encodeURIComponent(dataset).replace(/%2F/g, '/'),
      { sharename });
    showOpLog('SMB share enabled: ' + dataset, result.tasks, null);
    await _refreshSMBShares();
  } catch (e) {
    showOpLog('Failed to enable SMB share', e.tasks, e.message);
  }
});

document.getElementById('smbDisableBtn').addEventListener('click', async () => {
  const dataset = _smbDataset;
  const sharename = _smbCurrentSharename;
  if (!sharename) { smbDialog.close(); return; }
  smbDialog.close();
  showOpLogRunning('Disabling SMB share…');
  try {
    const result = await api('DELETE',
      '/api/smb-share/' + encodeURIComponent(dataset).replace(/%2F/g, '/') + '?name=' + encodeURIComponent(sharename));
    showOpLog('SMB share disabled: ' + dataset, result.tasks, null);
    await _refreshSMBShares();
  } catch (e) {
    showOpLog('Failed to disable SMB share', e.tasks, e.message);
  }
});

// ── Store subscriptions ──────────────────────────────────────────────────────
subscribe(['sysinfo'],                                          renderSysInfo);
subscribe(['sysinfo'],                                          renderSoftware);
subscribe(['pools', 'poolStatuses', 'scrubSchedules',
           'scrubScheduleMode', 'scrubThresholdDays'],          renderPools);
subscribe(['iostat'],                                           renderIOStat);
subscribe(['smart'],                                            renderSMART);
subscribe(['datasets', 'aclStatus', 'smbShares',
           'iscsiTargets', 'autoSnapshot'],                     renderDatasets);
subscribe(['snapshots'],                                        renderSnapshots);
subscribe(['users'],                                            renderUsers);
subscribe(['groups'],                                           renderGroups);
subscribe(['sambaUsers', 'sambaAvailable', 'users'],            renderSambaUsers);
subscribe(['smbHomes', 'datasets'],                             renderSMBHomes);
subscribe(['timeMachineShares'],                                renderTimeMachine);
subscribe(['schema'],                                           buildFormSelects);

// ── SSE client ────────────────────────────────────────────────────────────────
// Maps SSE topic names → state key (render is handled by store subscriptions).
const sseTopicMap = {
  'pool.query':         'pools',
  'poolstatus':         'poolStatuses',
  'dataset.query':      'datasets',
  'autosnapshot.query': 'autoSnapshot',
  'snapshot.query':     'snapshots',
  'iostat':             'iostat',
  'user.query':         'users',
  'group.query':        'groups',
};

let _pollInterval = null;  // setInterval handle; null when SSE is active
let _sseRetryTimer = null; // setTimeout handle for SSE reconnect attempts
let _es = null;            // active EventSource instance

function startPolling() {
  if (_pollInterval) return;
  _pollInterval = setInterval(loadAll, 30_000);
}

function stopPolling() {
  if (_pollInterval) { clearInterval(_pollInterval); _pollInterval = null; }
}

function startSSE() {
  if (_es) { _es.close(); _es = null; }
  const topics = [...Object.keys(sseTopicMap), 'ansible.progress'].join(',');
  const es = new EventSource('/api/events?topics=' + encodeURIComponent(topics));
  _es = es;

  es.onopen = () => {
    stopPolling();
    if (_sseRetryTimer) { clearTimeout(_sseRetryTimer); _sseRetryTimer = null; }
    // Always re-fetch all REST-only data (sysinfo, version, schema, smb config)
    // on every SSE (re)connect. SSE topics self-populate from the broker cache,
    // but endpoints not backed by SSE would otherwise stay stale/empty across
    // server restarts and post-reboot reconnects.
    loadAll();
  };

  es.addEventListener('ansible.progress', e => {
    try {
      const step = JSON.parse(e.data);
      // Only append if the dialog is open and still in running state (close button disabled)
      if (opLogDialog.open && document.getElementById('opLogClose').disabled) {
        appendOpLogStep(step);
      }
    } catch (err) { console.warn('[SSE] ansible.progress parse error', err); }
  });

  for (const [topic, key] of Object.entries(sseTopicMap)) {
    es.addEventListener(topic, e => {
      try {
        const v = JSON.parse(e.data);
        if (v !== null) storeSet(key, v);
      } catch (err) { console.warn('[SSE] parse error', topic, err); }
    });
  }

  es.onerror = () => {
    // Only fall back to polling when the browser has given up (CLOSED).
    // A transient CONNECTING state means the browser is already retrying.
    if (es.readyState === EventSource.CLOSED) {
      _es = null;
      startPolling();
      if (!_sseRetryTimer) {
        _sseRetryTimer = setTimeout(() => { _sseRetryTimer = null; startSSE(); }, 5_000);
      }
    }
  };
}

// ── iSCSI target dialog ───────────────────────────────────────────────────────
const iscsiDialog = document.getElementById('iscsiDialog');
document.getElementById('iscsiDialogClose').addEventListener('click', () => iscsiDialog.close());

let _iscsiDataset = '';
let _iscsiCurrentIQN = '';

document.getElementById('iscsiAuthMode').addEventListener('change', e => {
  document.getElementById('iscsiCHAPFields').style.display = e.target.value === 'chap' ? '' : 'none';
});

async function _refreshISCSITargets() {
  const targets = await api('GET', '/api/iscsi-targets').catch(() => []);
  storeSet('iscsiTargets', targets || []);
}

async function openISCSIDialog(dataset) {
  _iscsiDataset = dataset;
  _iscsiCurrentIQN = '';
  document.getElementById('iscsiDialogTitle').textContent = 'iSCSI Target \u2014 ' + dataset;
  document.getElementById('iscsiDialogStatus').innerHTML = '<span class="muted">Loading\u2026</span>';
  document.getElementById('iscsiCreateSection').style.display = 'none';
  document.getElementById('iscsiRemoveSection').style.display = 'none';
  iscsiDialog.showModal();

  try {
    const targets = await api('GET', '/api/iscsi-targets');
    storeSet('iscsiTargets', targets || []);
    const existing = state.iscsiTargets.find(t => t.zvol_name === dataset);

    if (existing) {
      _iscsiCurrentIQN = existing.iqn;
      document.getElementById('iscsiDialogStatus').innerHTML = `
        <div class="acl-entry" style="display:flex;align-items:center;gap:0.5rem;margin-bottom:0.4rem">
          <span class="field-note" style="width:5rem">IQN</span>
          <code style="flex:1;word-break:break-all">${esc(existing.iqn)}</code>
        </div>
        <div class="acl-entry" style="display:flex;align-items:center;gap:0.5rem;margin-bottom:0.4rem">
          <span class="field-note" style="width:5rem">Portals</span>
          <code>${esc((existing.portals || []).join(', ') || '\u2014')}</code>
        </div>
        <div class="acl-entry" style="display:flex;align-items:center;gap:0.5rem;margin-bottom:0.4rem">
          <span class="field-note" style="width:5rem">Auth</span>
          <code>${esc(existing.auth_mode || 'none')}</code>
        </div>
        <div class="acl-entry" style="display:flex;align-items:center;gap:0.5rem">
          <span class="field-note" style="width:5rem">Initiators</span>
          <code>${existing.initiators && existing.initiators.length ? esc(existing.initiators.join(', ')) : 'any (allow-all)'}</code>
        </div>`;
      document.getElementById('iscsiRemoveSection').style.display = '';
      document.getElementById('iscsiRemoveBtn').style.display = '';
      document.getElementById('iscsiExposeBtn').style.display = 'none';
    } else {
      // Auto-generate IQN from dataset name and current date.
      const now = new Date();
      const month = String(now.getMonth() + 1).padStart(2, '0');
      const year = now.getFullYear();
      const slug = dataset.replace(/\//g, '-');
      document.getElementById('iscsiIQN').value = `iqn.${year}-${month}.io.dumpstore:${slug}`;
      document.getElementById('iscsiPortalIP').value = '0.0.0.0';
      document.getElementById('iscsiPortalPort').value = '3260';
      document.getElementById('iscsiAuthMode').value = 'none';
      document.getElementById('iscsiCHAPFields').style.display = 'none';
      document.getElementById('iscsiCHAPUser').value = '';
      document.getElementById('iscsiCHAPPass').value = '';
      document.getElementById('iscsiInitiators').value = '';
      document.getElementById('iscsiDialogStatus').innerHTML = '<p class="muted">Not currently exposed as an iSCSI target.</p>';
      document.getElementById('iscsiCreateSection').style.display = '';
      document.getElementById('iscsiExposeBtn').style.display = '';
      document.getElementById('iscsiRemoveBtn').style.display = 'none';
    }
  } catch (e) {
    document.getElementById('iscsiDialogStatus').innerHTML = `<p class="op-error">${esc(e.message)}</p>`;
  }
}

document.getElementById('iscsiExposeBtn').addEventListener('click', async () => {
  const zvol = _iscsiDataset;
  const iqn = document.getElementById('iscsiIQN').value.trim();
  const portalIP = document.getElementById('iscsiPortalIP').value.trim() || '0.0.0.0';
  const portalPort = document.getElementById('iscsiPortalPort').value.trim() || '3260';
  const authMode = document.getElementById('iscsiAuthMode').value;
  const chapUser = document.getElementById('iscsiCHAPUser').value.trim();
  const chapPass = document.getElementById('iscsiCHAPPass').value;
  const initiators = document.getElementById('iscsiInitiators').value.trim()
    .split('\n').map(s => s.trim()).filter(Boolean);

  if (!iqn) { toast('IQN is required', 'error'); return; }

  iscsiDialog.close();
  showOpLogRunning('Creating iSCSI target\u2026');
  try {
    const result = await api('POST', '/api/iscsi-targets', {
      zvol,
      iqn,
      portal_ip: portalIP,
      portal_port: portalPort,
      auth_mode: authMode,
      chap_user: chapUser,
      chap_password: chapPass,
      initiators,
    });
    showOpLog('iSCSI target created: ' + iqn, result.tasks, null);
    await _refreshISCSITargets();
  } catch (e) {
    showOpLog('Failed to create iSCSI target', e.tasks, e.message);
  }
});

document.getElementById('iscsiRemoveBtn').addEventListener('click', async () => {
  const zvol = _iscsiDataset;
  const iqn = _iscsiCurrentIQN;
  iscsiDialog.close();
  showOpLogRunning('Removing iSCSI target\u2026');
  try {
    const result = await api('DELETE', `/api/iscsi-targets?iqn=${encodeURIComponent(iqn)}&zvol=${encodeURIComponent(zvol)}`);
    showOpLog('iSCSI target removed: ' + iqn, result.tasks, null);
    await _refreshISCSITargets();
  } catch (e) {
    showOpLog('Failed to remove iSCSI target', e.tasks, e.message);
  }
});

// ── Scrub schedule dialog wiring ──────────────────────────────────────────────
document.getElementById('scrubScheduleSaveBtn').addEventListener('click', saveScrubSchedule);
document.getElementById('scrubScheduleRemoveBtn').addEventListener('click', removeScrubSchedule);
document.getElementById('scrubScheduleCancelBtn').addEventListener('click', () => document.getElementById('scrubScheduleDialog').close());

async function removeAutoSnapSchedule() {
  const body = {};
  body['com.sun:auto-snapshot'] = '';
  _autoSnapPeriods.forEach(p => { body[p.prop] = ''; });
  document.getElementById('autoSnapDialog').close();
  showOpLogRunning(`Remove auto-snapshot: ${_autoSnapDataset}`);
  try {
    const encodedName = _autoSnapDataset.split('/').map(encodeURIComponent).join('/');
    const result = await api('PUT', '/api/auto-snapshot/' + encodedName, body);
    showOpLog(`Auto-snapshot removed: ${_autoSnapDataset}`, result.tasks, null);
    toast('Auto-snapshot config removed', 'ok');
  } catch (err) {
    showOpLog(`Auto-snapshot remove failed`, err.tasks, err.message);
  }
}

// ── Auto-snapshot dialog wiring ───────────────────────────────────────────────
document.getElementById('autoSnapSaveBtn').addEventListener('click', saveAutoSnapSchedule);
document.getElementById('autoSnapRemoveBtn').addEventListener('click', removeAutoSnapSchedule);
document.getElementById('autoSnapCancelBtn').addEventListener('click', () => document.getElementById('autoSnapDialog').close());

// ── Boot ──────────────────────────────────────────────────────────────────────
// Perform an immediate REST load so the UI is populated on first paint,
// then open the SSE stream. The SSE onopen handler cancels REST polling.
// If SSE is unavailable, startPolling() is called from the onerror handler.
//
// Safety-net: re-fetch all REST state every 60 s regardless of SSE health.
// SSE events can be silently dropped when the subscriber channel is full
// (broker logs "subscriber slow, dropping message"). When that happens the
// poller will not re-publish if the underlying data has not changed, leaving
// the browser permanently stale. This interval guarantees recovery.
setInterval(loadAll, 60_000);
loadAll();
startSSE();
