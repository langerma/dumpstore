import { state, storeSet, storeBatch } from './store.js';
import { api, delegate, esc, fmtBytes, fmtPct, fmtNum, fmtHours, fmtUptime, fmtSpeed, showOpLog, showOpLogRunning, toast } from './utils.js';
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

  const warningsHtml = s.warnings?.length ? `
    <div class="sysinfo-warnings">
      ${s.warnings.map(w => `<div class="sysinfo-warning">⚠ ${esc(w)}</div>`).join('')}
    </div>` : '';

  wrap.innerHTML = warningsHtml + `
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
      ${zfsInfoHtml()}
    </div>`;
}

// zfsInfoHtml renders the ZFS version and detected capabilities (#119)
// inside the sysinfo card. Empty until /api/version and /api/schema load.
function zfsInfoHtml() {
  const caps = state.schema?.capabilities;
  if (!state.version && !caps) return '';
  const capBadge = ok => `<span class="type-badge ${ok ? 'net-up' : 'net-down'}">${ok ? 'supported' : 'unsupported'}</span>`;
  const capItems = caps ? `
        <div class="si-item"><div class="si-label">zfs rewrite (≥ 2.3)</div><div class="si-value">${capBadge(caps.rewrite)}</div></div>
        <div class="si-item"><div class="si-label">draid (≥ 2.1)</div><div class="si-value">${capBadge(caps.draid)}</div></div>` : '';
  return `
      <div class="sysinfo-section-label" style="margin-top:0.75rem">ZFS</div>
      <div class="sysinfo-grid">
        <div class="si-item"><div class="si-label">Version</div><div class="si-value">${esc(state.version || '—')}</div></div>${capItems}
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

// ── Render: Network ───────────────────────────────────────────────────────────
export function renderNetwork() {
  const wrap = document.getElementById('network-wrap');
  if (!wrap) return;
  const ifaces = state.network;
  if (!ifaces) { wrap.innerHTML = ''; return; }
  if (!ifaces.length) { wrap.innerHTML = '<div class="loading">No interfaces found.</div>'; return; }

  const rows = ifaces.map(iface => {
    const muted = iface.virtual ? ' class="row-muted"' : '';
    const stateClass = iface.state === 'up' ? 'net-up' : 'net-down';
    const addrs = iface.addrs?.length ? iface.addrs.map(a => esc(a)).join('<br>') : '—';
    const rx = fmtBytes(iface.rx_bytes);
    const tx = fmtBytes(iface.tx_bytes);
    return `<tr${muted}>
      <td class="mono">${esc(iface.name)}</td>
      <td><span class="type-badge ${stateClass}">${esc(iface.state)}</span></td>
      <td class="mono">${iface.mac ? esc(iface.mac) : '—'}</td>
      <td>${iface.mtu}</td>
      <td class="mono">${addrs}</td>
      <td>${fmtSpeed(iface.speed_mbps)}</td>
      <td>${rx}</td>
      <td>${tx}</td>
    </tr>`;
  }).join('');

  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Interface</th><th>State</th><th>MAC</th><th>MTU</th><th>Addresses</th><th>Speed</th><th>RX</th><th>TX</th></tr></thead>
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
          ? `<button class="btn-secondary btn-sm" data-action="cancel-scrub" data-pool="${esc(p.name)}">Cancel Scrub</button>`
          : `<button class="btn-secondary btn-sm" data-action="start-scrub" data-pool="${esc(p.name)}">Start Scrub</button>`
        }
        <button class="btn-secondary btn-sm" data-action="scrub-schedule" data-pool="${esc(p.name)}">Schedule&hellip;</button>
        <button class="btn-secondary btn-sm" data-action="expand" data-pool="${esc(p.name)}">Expand&hellip;</button>
        <button class="btn-secondary btn-sm" data-action="export" data-pool="${esc(p.name)}">Export</button>
        ${schedBadge}
      </div>`;

    const statusLine = detail?.status
      ? `<div class="pool-status-msg">${esc(detail.status)}</div>`
      : '';

    const errLine = detail?.errors && detail.errors !== 'No known data errors'
      ? `<div class="pool-errors">${esc(detail.errors)}</div>`
      : '';

    const resilver = detail?.scan ? resilverInfoOf(detail.scan) : null;
    _trackResilver(p.name, resilver);
    const resilverLine = resilver?.state === 'in_progress' ? `
      <div class="resilver-label">Resilver in progress${resilver.pct != null ? ` — ${resilver.pct.toFixed(1)}%` : ''}</div>
      <div class="pool-bar-wrap"><div class="pool-bar warn" style="width:${Math.min(resilver.pct ?? 0, 100).toFixed(1)}%"></div></div>`
      : '';

    // Walk the flat vdev list tracking which zpool-status section we are in:
    // depth-0 rows are either the pool root (data section) or an auxiliary
    // section header (logs / cache / spares / special / dedup). Devices in
    // auxiliary sections get a Remove action; data devices get
    // Replace/Offline/Online.
    let vdevSectionName = 'data';
    const vdevRows = (detail?.vdevs || [])
      .map(v => {
        if (v.depth === 0) {
          if (['logs', 'cache', 'spares', 'special', 'dedup'].includes(v.name)) {
            vdevSectionName = v.name;
            return `<div class="vdev-row vdev-section-label">${esc(v.name)}</div>`;
          }
          vdevSectionName = 'data'; // pool root row — not rendered
          return '';
        }
        const indent = v.depth - 1;
        const errs = v.read || v.write || v.cksum
          ? `<span class="vdev-errs">${v.read}/${v.write}/${v.cksum}</span>`
          : '';
        // Grouping vdevs (mirror-0, raidz1-0, …) take no device actions.
        const isLeaf = !/^(mirror|raidz|draid|spare|replacing|logs|cache|special|dedup)/.test(v.name);
        let actions = '';
        if (isLeaf && ['logs', 'cache', 'spares'].includes(vdevSectionName)) {
          actions = `
          <span class="vdev-actions">
            <button class="btn-vdev" data-action="remove-device" data-pool="${esc(p.name)}" data-device="${esc(v.name)}">Remove</button>
          </span>`;
        } else if (isLeaf && vdevSectionName === 'data') {
          actions = `
          <span class="vdev-actions">
            <button class="btn-vdev" data-action="replace" data-pool="${esc(p.name)}" data-device="${esc(v.name)}">Replace</button>
            ${v.state === 'OFFLINE'
              ? `<button class="btn-vdev" data-action="online" data-pool="${esc(p.name)}" data-device="${esc(v.name)}">Online</button>`
              : `<button class="btn-vdev" data-action="offline" data-pool="${esc(p.name)}" data-device="${esc(v.name)}">Offline</button>`}
          </span>`;
        }
        return `
          <div class="vdev-row" style="--vdepth:${indent}">
            <span class="vdev-name">${esc(v.name)}</span>
            <span class="vdev-state state-${esc(v.state || 'UNKNOWN')}">${esc(v.state || '—')}</span>
            ${errs}${actions}
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
        ${resilverLine}${scanLine}${scrubActions}${statusLine}${errLine}${vdevSection}
      </div>`;
  }).join('');
}

// One delegated listener on the stable grid; survives every render.
delegate(document.getElementById('pools-grid'), {
  'start-scrub':    ({ pool }) => startScrub(pool),
  'cancel-scrub':   ({ pool }) => cancelScrub(pool),
  'scrub-schedule': ({ pool }) => openScrubScheduleDialog(pool),
  'expand':         ({ pool }) => openExpandPoolDialog(pool),
  'export':         ({ pool }) => exportPool(pool),
  'replace':        ({ pool, device }) => openReplaceDeviceDialog(pool, device),
  'offline':        ({ pool, device }) => offlineDevice(pool, device),
  'online':         ({ pool, device }) => onlineDevice(pool, device),
  'remove-device':  ({ pool, device }) => removePoolDevice(pool, device),
});

// ── Resilver helpers ──────────────────────────────────────────────────────────
// Parses the raw scan string from `zpool status`. Returns
// { state: 'in_progress', pct } while a resilver runs, { state: 'done' } right
// after one finished, or null when the scan line is about something else.
function resilverInfoOf(scan) {
  if (!scan) return null;
  if (scan.startsWith('resilver in progress')) {
    const m = scan.match(/([\d.]+)% done/);
    return { state: 'in_progress', pct: m ? parseFloat(m[1]) : null };
  }
  if (scan.startsWith('resilvered')) return { state: 'done' };
  return null;
}

// Tracks pools with an active resilver so completion can be announced when a
// poolstatus SSE update flips the scan line from in-progress to resilvered.
const _resilveringPools = new Set();

function _trackResilver(pool, resilver) {
  if (resilver?.state === 'in_progress') {
    _resilveringPools.add(pool);
  } else if (_resilveringPools.has(pool)) {
    _resilveringPools.delete(pool);
    toast(`Resilver finished on ${pool}`, 'ok');
  }
}

// ── Device actions (replace / offline / online) ───────────────────────────────
let _replaceCtx = { pool: '', old: '' };

async function openReplaceDeviceDialog(pool, oldDev) {
  _replaceCtx = { pool, old: oldDev };
  document.getElementById('replaceDevicePool').textContent = pool;
  document.getElementById('replaceDeviceOld').textContent = oldDev;
  document.getElementById('replaceDeviceManual').value = '';
  const sel = document.getElementById('replaceDeviceNew');
  sel.innerHTML = '<option value="">Loading devices&hellip;</option>';
  document.getElementById('replaceDeviceDialog').showModal();
  try {
    const devs = await api('GET', '/api/devices');
    const free = (devs || []).filter(d => !d.in_use_by);
    sel.innerHTML = free.length
      ? '<option value="">— select a device —</option>' + free.map(d =>
          `<option value="${esc(d.path)}">${esc(d.path)} · ${fmtBytes(d.size_bytes)}${d.model ? ' · ' + esc(d.model) : ''}</option>`).join('')
      : '<option value="">No unused devices found</option>';
  } catch (err) {
    sel.innerHTML = '<option value="">Failed to list devices</option>';
  }
}

async function confirmReplaceDevice() {
  const manual = document.getElementById('replaceDeviceManual').value.trim();
  const newDev = manual || document.getElementById('replaceDeviceNew').value;
  if (!newDev) { toast('Choose a replacement device', 'err'); return; }
  const { pool, old } = _replaceCtx;
  document.getElementById('replaceDeviceDialog').close();
  showOpLogRunning(`Replace ${old} in ${pool}`);
  try {
    const data = await api('POST', `/api/pools/${encodeURIComponent(pool)}/replace`,
      { old_device: old, new_device: newDev });
    showOpLog(`Replace ${old} in ${pool}`, data.tasks, null);
    toast(`Replace started on ${pool} — resilver running`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(`Replace ${old} in ${pool}`, err.tasks, err.message);
  }
}

async function offlineDevice(pool, device) {
  if (!confirm(`Take ${device} offline?\n\nPool ${pool} will run without this device — redundancy is reduced until it is brought back online.`)) return;
  await _deviceStateOp(pool, device, 'offline');
}

async function onlineDevice(pool, device) {
  await _deviceStateOp(pool, device, 'online');
}

async function _deviceStateOp(pool, device, op) {
  const title = `${op === 'offline' ? 'Offline' : 'Online'} ${device} (${pool})`;
  showOpLogRunning(title);
  try {
    const data = await api('POST', `/api/pools/${encodeURIComponent(pool)}/${op}`, { device });
    showOpLog(title, data.tasks, null);
    toast(`${device} is now ${op}`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(title, err.tasks, err.message);
  }
}

// ── Replace-device dialog wiring ──────────────────────────────────────────────
document.getElementById('replaceDeviceConfirmBtn').addEventListener('click', confirmReplaceDevice);
document.getElementById('replaceDeviceCancelBtn').addEventListener('click', () => document.getElementById('replaceDeviceDialog').close());

// ── Pool lifecycle (create / import / export) ─────────────────────────────────
const reZFSPoolName = /^[a-zA-Z][a-zA-Z0-9_.:-]*$/;

const _vdevMinDevices = {
  single: 1, mirror: 2, raidz1: 2, raidz2: 3, raidz3: 4, draid: 2, draid2: 3, draid3: 4,
};

async function openCreatePoolDialog() {
  document.getElementById('createPoolName').value = '';
  document.getElementById('createPoolType').value = 'mirror';
  document.getElementById('createPoolAshift').value = '';
  document.getElementById('createPoolCompression').value = '';
  document.getElementById('createPoolConfirmInput').value = '';
  document.getElementById('createPoolConfirmBtn').disabled = true;
  const picker = document.getElementById('createPoolDevices');
  picker.innerHTML = '<div class="loading">Loading devices&hellip;</div>';
  document.getElementById('createPoolDialog').showModal();
  try {
    const devs = await api('GET', '/api/devices');
    const free = (devs || []).filter(d => !d.in_use_by);
    picker.innerHTML = free.length
      ? free.map(d => `
          <label class="checkbox-label">
            <input type="checkbox" class="create-pool-dev" value="${esc(d.path)}">
            <span class="mono">${esc(d.path)}</span> · ${fmtBytes(d.size_bytes)}${d.model ? ' · ' + esc(d.model) : ''}
          </label>`).join('')
      : '<div class="loading">No unused devices found.</div>';
  } catch (err) {
    picker.innerHTML = '<div class="loading">Failed to list devices.</div>';
  }
}

function _syncCreatePoolConfirm() {
  const name = document.getElementById('createPoolName').value.trim();
  const typed = document.getElementById('createPoolConfirmInput').value.trim();
  document.getElementById('createPoolConfirmBtn').disabled = !(name && typed === name);
}
document.getElementById('createPoolName').addEventListener('input', _syncCreatePoolConfirm);
document.getElementById('createPoolConfirmInput').addEventListener('input', _syncCreatePoolConfirm);

async function confirmCreatePool() {
  const name = document.getElementById('createPoolName').value.trim();
  const vdevType = document.getElementById('createPoolType').value;
  const devices = [...document.querySelectorAll('.create-pool-dev:checked')].map(cb => cb.value);
  if (!reZFSPoolName.test(name)) { toast('Invalid pool name', 'err'); return; }
  const min = _vdevMinDevices[vdevType] || 1;
  if (devices.length < min) { toast(`${vdevType} needs at least ${min} device${min === 1 ? '' : 's'}`, 'err'); return; }
  document.getElementById('createPoolDialog').close();
  showOpLogRunning(`Create pool: ${name}`);
  try {
    const data = await api('POST', '/api/pools', {
      name,
      vdev_type: vdevType,
      devices,
      ashift: document.getElementById('createPoolAshift').value,
      compression: document.getElementById('createPoolCompression').value,
    });
    showOpLog(`Create pool: ${name}`, data.tasks, null);
    toast(`Pool ${name} created`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(`Create pool: ${name}`, err.tasks, err.message);
  }
}

document.getElementById('createPoolBtn').addEventListener('click', openCreatePoolDialog);
document.getElementById('createPoolConfirmBtn').addEventListener('click', confirmCreatePool);
document.getElementById('createPoolCancelBtn').addEventListener('click', () => document.getElementById('createPoolDialog').close());

let _importPoolSelected = '';

async function openImportPoolDialog() {
  _importPoolSelected = '';
  document.getElementById('importPoolForce').checked = false;
  document.getElementById('importPoolConfirmBtn').disabled = true;
  const list = document.getElementById('importPoolList');
  list.innerHTML = '<div class="loading">Scanning for importable pools&hellip;</div>';
  document.getElementById('importPoolDialog').showModal();
  try {
    const pools = await api('GET', '/api/pools/importable');
    if (!pools?.length) {
      list.innerHTML = '<div class="loading">No importable pools found.</div>';
      return;
    }
    list.innerHTML = `
      <table>
        <thead><tr><th></th><th>Pool</th><th>State</th><th>ID</th></tr></thead>
        <tbody>${pools.map(p => `
          <tr>
            <td><input type="radio" name="importPoolPick" value="${esc(p.name)}"></td>
            <td class="mono">${esc(p.name)}</td>
            <td><span class="health-badge health-${esc(p.state)}">${esc(p.state)}</span></td>
            <td class="muted mono">${esc(p.id)}</td>
          </tr>${p.status ? `<tr><td></td><td colspan="3" class="muted">${esc(p.status)}</td></tr>` : ''}`).join('')}
        </tbody>
      </table>`;
    list.querySelectorAll('input[name=importPoolPick]').forEach(rb => {
      rb.addEventListener('change', () => {
        _importPoolSelected = rb.value;
        document.getElementById('importPoolConfirmBtn').disabled = false;
      });
    });
  } catch (err) {
    list.innerHTML = `<div class="loading">Scan failed: ${esc(err.message)}</div>`;
  }
}

async function confirmImportPool() {
  const pool = _importPoolSelected;
  if (!pool) return;
  const force = document.getElementById('importPoolForce').checked;
  document.getElementById('importPoolDialog').close();
  showOpLogRunning(`Import pool: ${pool}`);
  try {
    const data = await api('POST', '/api/pools/import', { pool, force });
    showOpLog(`Import pool: ${pool}`, data.tasks, null);
    toast(`Pool ${pool} imported`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(`Import pool: ${pool}`, err.tasks, err.message);
  }
}

document.getElementById('importPoolBtn').addEventListener('click', openImportPoolDialog);
document.getElementById('importPoolConfirmBtn').addEventListener('click', confirmImportPool);
document.getElementById('importPoolCancelBtn').addEventListener('click', () => document.getElementById('importPoolDialog').close());

async function exportPool(pool) {
  if (!confirm(`Export pool ${pool}?\n\nAll datasets are unmounted and the pool disappears from this host until it is re-imported. The export fails if the pool is busy (open files, active shares, running jobs).`)) return;
  showOpLogRunning(`Export pool: ${pool}`);
  try {
    const data = await api('POST', `/api/pools/${encodeURIComponent(pool)}/export`);
    showOpLog(`Export pool: ${pool}`, data.tasks, null);
    toast(`Pool ${pool} exported`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(`Export pool: ${pool}`, err.tasks, err.message);
  }
}

// ── Pool expansion (add vdev / cache / log / spare, remove aux device) ────────
let _expandPool = '';

async function openExpandPoolDialog(pool) {
  _expandPool = pool;
  document.getElementById('expandPoolName').textContent = pool;
  document.getElementById('expandPoolKind').value = 'data';
  document.getElementById('expandPoolVdevType').value = 'mirror';
  document.getElementById('expandPoolLogMirror').checked = false;
  document.getElementById('expandPoolConfirmInput').value = '';
  _syncExpandPoolUI();
  const picker = document.getElementById('expandPoolDevices');
  picker.innerHTML = '<div class="loading">Loading devices&hellip;</div>';
  document.getElementById('expandPoolDialog').showModal();
  try {
    const devs = await api('GET', '/api/devices');
    const free = (devs || []).filter(d => !d.in_use_by);
    picker.innerHTML = free.length
      ? free.map(d => `
          <label class="checkbox-label">
            <input type="checkbox" class="expand-pool-dev" value="${esc(d.path)}">
            <span class="mono">${esc(d.path)}</span> · ${fmtBytes(d.size_bytes)}${d.model ? ' · ' + esc(d.model) : ''}
          </label>`).join('')
      : '<div class="loading">No unused devices found.</div>';
  } catch (err) {
    picker.innerHTML = '<div class="loading">Failed to list devices.</div>';
  }
}

// Data vdev additions are irreversible — they need the typed confirmation.
// The vdev-type select only applies to data; the mirror checkbox only to log.
function _syncExpandPoolUI() {
  const kind = document.getElementById('expandPoolKind').value;
  document.getElementById('expandPoolVdevTypeRow').style.display = kind === 'data' ? '' : 'none';
  document.getElementById('expandPoolLogMirrorRow').style.display = kind === 'log' ? '' : 'none';
  document.getElementById('expandPoolDataWarning').style.display = kind === 'data' ? '' : 'none';
  document.getElementById('expandPoolConfirmRow').style.display = kind === 'data' ? '' : 'none';
  _syncExpandPoolConfirm();
}

function _syncExpandPoolConfirm() {
  const kind = document.getElementById('expandPoolKind').value;
  const typed = document.getElementById('expandPoolConfirmInput').value.trim();
  document.getElementById('expandPoolConfirmBtn').disabled = kind === 'data' && typed !== _expandPool;
}

document.getElementById('expandPoolKind').addEventListener('change', _syncExpandPoolUI);
document.getElementById('expandPoolConfirmInput').addEventListener('input', _syncExpandPoolConfirm);

async function confirmExpandPool() {
  const kind = document.getElementById('expandPoolKind').value;
  const devices = [...document.querySelectorAll('.expand-pool-dev:checked')].map(cb => cb.value);
  if (!devices.length) { toast('Select at least one device', 'err'); return; }

  let path, body;
  if (kind === 'data') {
    const vdevType = document.getElementById('expandPoolVdevType').value;
    const min = _vdevMinDevices[vdevType] || 1;
    if (devices.length < min) { toast(`${vdevType} needs at least ${min} device${min === 1 ? '' : 's'}`, 'err'); return; }
    path = 'vdevs';
    body = { vdev_type: vdevType, devices };
  } else if (kind === 'log') {
    const mirror = document.getElementById('expandPoolLogMirror').checked;
    if (mirror && devices.length < 2) { toast('A mirrored log needs at least 2 devices', 'err'); return; }
    path = 'log';
    body = { devices, mirror };
  } else {
    path = kind; // cache | spare
    body = { devices };
  }

  document.getElementById('expandPoolDialog').close();
  const title = `Add ${kind} to ${_expandPool}`;
  showOpLogRunning(title);
  try {
    const data = await api('POST', `/api/pools/${encodeURIComponent(_expandPool)}/${path}`, body);
    showOpLog(title, data.tasks, null);
    toast(`Devices added to ${_expandPool}`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(title, err.tasks, err.message);
  }
}

document.getElementById('expandPoolConfirmBtn').addEventListener('click', confirmExpandPool);
document.getElementById('expandPoolCancelBtn').addEventListener('click', () => document.getElementById('expandPoolDialog').close());

async function removePoolDevice(pool, device) {
  if (!confirm(`Remove ${device} from pool ${pool}?`)) return;
  const title = `Remove ${device} from ${pool}`;
  showOpLogRunning(title);
  try {
    const encDev = device.split('/').map(encodeURIComponent).join('/');
    const data = await api('DELETE', `/api/pools/${encodeURIComponent(pool)}/devices/${encDev}`);
    showOpLog(title, data.tasks, null);
    toast(`${device} removed from ${pool}`, 'ok');
    await loadAll();
  } catch (err) {
    showOpLog(title, err.tasks, err.message);
  }
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
