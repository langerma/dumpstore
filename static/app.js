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
  activeTab: 'pools',
  collapsedDatasets: new Set(),
};

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
  opLogDialog.showModal();
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

// ── Load all ──────────────────────────────────────────────────────────────────
async function loadAll() {
  setRefreshing(true);
  try {
    const [pools, poolStatuses, version, sysinfo, datasets, snapshots, iostat, smart] = await Promise.all([
      api('GET', '/api/pools'),
      api('GET', '/api/poolstatus'),
      api('GET', '/api/version'),
      api('GET', '/api/sysinfo'),
      api('GET', '/api/datasets'),
      api('GET', '/api/snapshots'),
      api('GET', '/api/iostat'),
      api('GET', '/api/smart'),
    ]);
    state.pools = pools || [];
    state.poolStatuses = poolStatuses || [];
    state.version = version?.version || '';
    state.sysinfo = sysinfo || null;
    state.datasets = datasets || [];
    state.snapshots = snapshots || [];
    state.iostat = iostat || [];
    state.smart = smart || null;
    renderPools();
    renderSysInfo();
    renderDatasets();
    renderSnapshots();
    renderIOStat();
    renderSMART();
  } catch (e) {
    toast('Load failed: ' + e.message, 'err');
    console.error(e);
  } finally {
    setRefreshing(false);
  }
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

// ── Render: Pools ─────────────────────────────────────────────────────────────
function renderPools() {
  const grid = document.getElementById('pools-grid');

  // Version bar
  const verEl = document.getElementById('zfsVersionBar');
  if (verEl) {
    const parts = [];
    if (state.sysinfo?.app_version) parts.push(`dumpstore ${esc(state.sysinfo.app_version)}`);
    if (state.version) parts.push(`OpenZFS ${esc(state.version)}`);
    verEl.textContent = parts.join(' · ');
  }

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

    const scanLine = detail?.scan
      ? `<div class="pool-scan">${esc(detail.scan)}</div>`
      : '';

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
        ${scanLine}${statusLine}${errLine}${vdevSection}
      </div>`;
  }).join('');
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
        <td class="muted">${esc(d.name)}</td>
        <td>${fmtBytes(d.used)}</td>
        <td>${fmtBytes(d.avail)}</td>
        <td>${fmtBytes(d.refer)}</td>
        <td class="muted">${esc(d.compression)}</td>
        <td class="muted">${esc(d.compress_ratio)}</td>
        <td class="muted">${d.mountpoint !== 'none' ? esc(d.mountpoint) : '—'}</td>
        <td>
          <div class="row-actions">
            <button class="btn-edit" data-ds="${esc(d.name)}" data-type="${esc(d.type)}">Edit</button>
            ${canDelete ? `<button class="btn-del" data-ds="${esc(d.name)}" data-type="${esc(d.type)}">Delete</button>` : ''}
          </div>
        </td>
      </tr>`;
  }).join('');

  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Name</th><th>Full Path</th><th>Used</th><th>Avail</th>
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

function renderSnapshots() {
  const wrap = document.getElementById('snapshots-table-wrap');
  let items = state.snapshots;
  if (snapFilter) {
    items = items.filter(s => s.name.toLowerCase().includes(snapFilter));
  }
  if (!items.length) {
    wrap.innerHTML = '<div class="loading">No snapshots found.</div>';
    return;
  }
  const rows = items.map(s => `
    <tr>
      <td class="mono">${esc(s.dataset)}</td>
      <td class="mono">${esc(s.snap_label)}</td>
      <td>${fmtBytes(s.used)}</td>
      <td>${fmtBytes(s.refer)}</td>
      <td class="muted">${fmtDate(s.creation)}</td>
      <td class="muted">${s.clones && s.clones !== '-' ? esc(s.clones) : '—'}</td>
      <td><button class="btn-del" data-snap="${esc(s.name)}">Delete</button></td>
    </tr>`).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Dataset</th><th>Snapshot</th><th>Used</th>
          <th>Refer</th><th>Created</th><th>Clones</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;

  wrap.querySelectorAll('.btn-del').forEach(btn => {
    btn.addEventListener('click', () => deleteSnapshot(btn.dataset.snap));
  });
}

async function deleteSnapshot(name) {
  if (!confirm(`Delete snapshot ${name}?`)) return;
  try {
    const result = await api('DELETE', '/api/snapshots/' + encodeURIComponent(name));
    state.snapshots = state.snapshots.filter(s => s.name !== name);
    renderSnapshots();
    showOpLog(`Deleted snapshot: ${name}`, result.tasks, null);
  } catch (e) {
    showOpLog('Snapshot deletion failed', e.tasks, e.message);
  }
}

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
  try {
    const result = await api('POST', '/api/snapshots', { dataset, snapname, recursive });
    showOpLog(`Snapshot: ${dataset}@${snapname}`, result.tasks, null);
    const snaps = await api('GET', '/api/snapshots');
    state.snapshots = snaps || [];
    renderSnapshots();
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
    name:         document.getElementById('ds-name').value.trim(),
    type:         document.getElementById('ds-type').value,
    volsize:      document.getElementById('ds-volsize').value.trim(),
    volblocksize: document.getElementById('ds-volblocksize').value,
    sparse:       document.getElementById('ds-sparse').checked,
    compression:  document.getElementById('ds-compression').value,
    quota:        document.getElementById('ds-quota').value.trim(),
    mountpoint:   document.getElementById('ds-mountpoint').value.trim(),
    recordsize:   document.getElementById('ds-recordsize').value,
    atime:        document.getElementById('ds-atime').value,
    exec:         document.getElementById('ds-exec').value,
    sync:         document.getElementById('ds-sync').value,
    dedup:        document.getElementById('ds-dedup').value,
    copies:       document.getElementById('ds-copies').value,
    xattr:        document.getElementById('ds-xattr').value,
  };
  datasetDialog.close();
  try {
    const result = await api('POST', '/api/datasets', body);
    showOpLog(`Dataset created: ${body.name}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    state.datasets = datasets || [];
    renderDatasets();
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
  try {
    const encodedName = name.split('/').map(encodeURIComponent).join('/');
    const url = '/api/datasets/' + encodedName + (recursive ? '?recursive=true' : '');
    const result = await api('DELETE', url);
    showOpLog(`Deleted dataset: ${name}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    state.datasets = datasets || [];
    state.collapsedDatasets.delete(name);
    renderDatasets();
  } catch (e) {
    showOpLog(`Failed to delete ${name}`, e.tasks, e.message);
  }
});

// ── Edit Dataset dialog ───────────────────────────────────────────────────────
const editDatasetDialog = document.getElementById('editDatasetDialog');
let _editDatasetName = '';
let _editDatasetType = '';
let _editOriginalProps = {};  // prop → display value at open time

// Select fields managed by the edit dialog.
const _editSelectFields = ['compression', 'atime', 'exec', 'sync', 'dedup', 'copies', 'xattr', 'readonly', 'recordsize'];
// Text input fields managed by the edit dialog.
const _editTextFields = ['quota', 'mountpoint'];

document.getElementById('editDatasetCancelBtn').addEventListener('click', () => editDatasetDialog.close());

async function openEditDatasetDialog(name, type) {
  _editDatasetName = name;
  _editDatasetType = type;
  document.getElementById('editDatasetTitle').textContent = `Edit: ${name}`;

  // Show/hide filesystem-only section.
  document.getElementById('edit-ds-fs-section').style.display = type === 'filesystem' ? '' : 'none';

  // Reset form before fetching.
  for (const f of [..._editSelectFields, ..._editTextFields]) {
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
  for (const f of _editSelectFields) {
    const el = document.getElementById('edit-ds-' + f);
    if (!el) continue;
    const val = el.value;
    if (val !== (_editOriginalProps[f] ?? '')) body[f] = val;
  }
  for (const f of _editTextFields) {
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
  try {
    const encodedName = _editDatasetName.split('/').map(encodeURIComponent).join('/');
    const result = await api('PATCH', '/api/datasets/' + encodedName, body);
    showOpLog(`Properties updated: ${_editDatasetName}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    state.datasets = datasets || [];
    renderDatasets();
  } catch (e) {
    showOpLog(`Failed to update ${_editDatasetName}`, e.tasks, e.message);
  }
});

// ── XSS helper ────────────────────────────────────────────────────────────────
function esc(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ── SSE client ────────────────────────────────────────────────────────────────
// Maps SSE topic names → state key + render function.
const sseTopicMap = {
  'pool.query':     { key: 'pools',     render: renderPools },
  'dataset.query':  { key: 'datasets',  render: renderDatasets },
  'snapshot.query': { key: 'snapshots', render: renderSnapshots },
  'iostat':         { key: 'iostat',    render: renderIOStat },
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
  const topics = Object.keys(sseTopicMap).join(',');
  const es = new EventSource('/api/events?topics=' + encodeURIComponent(topics));
  _es = es;

  es.onopen = () => {
    stopPolling();
    if (_sseRetryTimer) { clearTimeout(_sseRetryTimer); _sseRetryTimer = null; }
  };

  for (const [topic, { key, render }] of Object.entries(sseTopicMap)) {
    es.addEventListener(topic, e => {
      try { state[key] = JSON.parse(e.data); render(); }
      catch (err) { console.warn('[SSE] parse error', topic, err); }
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

// ── Boot ──────────────────────────────────────────────────────────────────────
// Perform an immediate REST load so the UI is populated on first paint,
// then open the SSE stream. The SSE onopen handler cancels REST polling.
// If SSE is unavailable, startPolling() is called from the onerror handler.
loadAll();
startSSE();
