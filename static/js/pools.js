import { state, storeSet, storeBatch } from './store.js';
import { api, esc, fmtBytes, fmtPct, fmtNum, fmtHours, fmtUptime, showOpLog, showOpLogRunning, toast } from './utils.js';
import { loadAll } from './loader.js';

// ── Render: System info ────────────────────────────────────────────────────────
export function renderSysInfo() {
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
export function renderSoftware() {
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
export function renderPools() {
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
          ? `<button class="btn-secondary btn-sm" onclick="cancelScrub('${esc(p.name)}')">Cancel Scrub</button>`
          : `<button class="btn-secondary btn-sm" onclick="startScrub('${esc(p.name)}')">Start Scrub</button>`
        }
        <button class="btn-secondary btn-sm" onclick="openScrubScheduleDialog('${esc(p.name)}')">Schedule&hellip;</button>
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

// ── Scrub schedule dialog wiring ──────────────────────────────────────────────
document.getElementById('scrubScheduleSaveBtn').addEventListener('click', saveScrubSchedule);
document.getElementById('scrubScheduleRemoveBtn').addEventListener('click', removeScrubSchedule);
document.getElementById('scrubScheduleCancelBtn').addEventListener('click', () => document.getElementById('scrubScheduleDialog').close());

// ── Auto-snapshot schedule helpers ────────────────────────────────────────────
const _autoSnapPeriods = [
  { prop: 'com.sun:auto-snapshot:frequent', label: 'Frequent (15 min)', id: 'autosnap-frequent' },
  { prop: 'com.sun:auto-snapshot:hourly',   label: 'Hourly',            id: 'autosnap-hourly'   },
  { prop: 'com.sun:auto-snapshot:daily',    label: 'Daily',             id: 'autosnap-daily'    },
  { prop: 'com.sun:auto-snapshot:weekly',   label: 'Weekly',            id: 'autosnap-weekly'   },
  { prop: 'com.sun:auto-snapshot:monthly',  label: 'Monthly',           id: 'autosnap-monthly'  },
];

let _autoSnapDataset = '';

export async function openAutoSnapDialog(name) {
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

// ── Render: I/O Stats ─────────────────────────────────────────────────────────
export function renderIOStat() {
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

// ── Render: Disk Health (SMART) ───────────────────────────────────────────────
export function renderSMART() {
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

// ── window assignments for inline onclick handlers and cross-module calls ─────
window.startScrub = startScrub;
window.cancelScrub = cancelScrub;
window.openScrubScheduleDialog = openScrubScheduleDialog;
window.openAutoSnapDialog = openAutoSnapDialog;
