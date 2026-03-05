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
  activeTab: 'pools',
  collapsedDatasets: new Set(),
  aclDataset: '',
  aclData: null,
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
    const [pools, poolStatuses, version, sysinfo, datasets, snapshots, iostat, smart, users, groups] = await Promise.all([
      api('GET', '/api/pools'),
      api('GET', '/api/poolstatus'),
      api('GET', '/api/version'),
      api('GET', '/api/sysinfo'),
      api('GET', '/api/datasets'),
      api('GET', '/api/snapshots'),
      api('GET', '/api/iostat'),
      api('GET', '/api/smart'),
      api('GET', '/api/users'),
      api('GET', '/api/groups'),
    ]);
    state.pools = pools || [];
    state.poolStatuses = poolStatuses || [];
    state.version = version?.version || '';
    state.sysinfo = sysinfo || null;
    state.datasets = datasets || [];
    state.snapshots = snapshots || [];
    state.iostat = iostat || [];
    state.smart = smart || null;
    state.users = users || [];
    state.groups = groups || [];
    renderPools();
    renderSysInfo();
    renderSoftware();
    renderDatasets();
    renderSnapshots();
    renderIOStat();
    renderSMART();
    renderUsers();
    renderGroups();
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
            ${d.type !== 'volume' ? `<button class="btn-acl btn-small" data-ds="${esc(d.name)}">ACL</button>` : ''}
            ${d.type === 'filesystem' && d.mountpoint !== 'none' && d.mountpoint !== '-' ? `<button class="btn-chown btn-small" data-ds="${esc(d.name)}">Chown</button>` : ''}
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

  wrap.querySelectorAll('.btn-acl[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openACLDialog(btn.dataset.ds));
  });

  wrap.querySelectorAll('.btn-chown[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openChownDialog(btn.dataset.ds));
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

// ── Protected account denylists (must match handlers.go) ─────────────────────
const PROTECTED_USERS  = new Set(['nobody', 'nfsnobody']);
const PROTECTED_GROUPS = new Set(['nogroup', 'nobody', 'nfsnobody']);

// ── Render: Users ─────────────────────────────────────────────────────────────
function renderUsers() {
  const wrap = document.getElementById('users-table-wrap');
  if (!state.users.length) {
    wrap.innerHTML = '<div class="loading">No users found.</div>';
    return;
  }
  const rows = state.users.map(u => {
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
  const wrap = document.getElementById('groups-table-wrap');
  if (!state.groups.length) {
    wrap.innerHTML = '<div class="loading">No groups found.</div>';
    return;
  }
  const rows = state.groups.map(g => {
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
  const username = document.getElementById('user-name').value.trim();
  const shell    = document.getElementById('user-shell').value;
  const uid      = document.getElementById('user-uid').value.trim();
  const group    = document.getElementById('user-group').value.trim();
  const groups   = document.getElementById('user-groups').value.trim();
  const password = document.getElementById('user-password').value;
  const createGroup = document.getElementById('user-create-group').checked;
  newUserDialog.close();
  try {
    const result = await api('POST', '/api/users', { username, shell, uid, group, groups, password, create_group: createGroup });
    showOpLog(`User created: ${username}`, result.tasks, null);
    const users = await api('GET', '/api/users');
    state.users = users || [];
    renderUsers();
  } catch (e) {
    showOpLog('User creation failed', e.tasks, e.message);
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
  try {
    const result = await api('DELETE', '/api/users/' + encodeURIComponent(username));
    showOpLog(`Deleted user: ${username}`, result.tasks, null);
    const users = await api('GET', '/api/users');
    state.users = users || [];
    renderUsers();
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
  try {
    const result = await api('POST', '/api/groups', { groupname, gid });
    showOpLog(`Group created: ${groupname}`, result.tasks, null);
    const groups = await api('GET', '/api/groups');
    state.groups = groups || [];
    renderGroups();
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
  try {
    const result = await api('DELETE', '/api/groups/' + encodeURIComponent(groupname));
    showOpLog(`Deleted group: ${groupname}`, result.tasks, null);
    const groups = await api('GET', '/api/groups');
    state.groups = groups || [];
    renderGroups();
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
  try {
    const result = await api('PUT', '/api/users/' + encodeURIComponent(username), { shell, group, user_groups, password });
    showOpLog(`User updated: ${username}`, result.tasks, null);
    const [users, groups] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/groups')]);
    state.users = users || [];
    state.groups = groups || [];
    renderUsers();
    renderGroups();
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
  try {
    const result = await api('PUT', '/api/groups/' + encodeURIComponent(groupname), { new_name, gid, members });
    showOpLog(`Group updated: ${result.groupname}`, result.tasks, null);
    const [users, groups] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/groups')]);
    state.users = users || [];
    state.groups = groups || [];
    renderUsers();
    renderGroups();
  } catch (err) {
    showOpLog(`Failed to update group: ${groupname}`, err.tasks, err.message);
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

function renderPOSIXAddForm(d) {
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
  const listEl = document.getElementById('aclUserList');

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

function renderNFSv4AddForm(d) {
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

async function addACLEntry(ace, recursive) {
  const dataset = state.aclDataset;
  try {
    const res = await api('POST', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { ace, recursive });
    showOpLog(`ACL entry added — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
  } catch (err) {
    showOpLog(`Failed to add ACL entry`, err.tasks, err.message);
  }
}

async function removeACLEntry(entry, recursive) {
  const dataset = state.aclDataset;
  try {
    const qs = `entry=${encodeURIComponent(entry)}${recursive ? '&recursive=true' : ''}`;
    const res = await api('DELETE', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}?${qs}`);
    showOpLog(`ACL entry removed — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
  } catch (err) {
    showOpLog(`Failed to remove ACL entry`, err.tasks, err.message);
  }
}

async function enableACLs(dataset, acltype) {
  try {
    const res = await api('PATCH', `/api/datasets/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { acltype });
    showOpLog(`Enabled ${acltype} ACLs — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
  } catch (err) {
    showOpLog(`Failed to enable ACLs`, err.tasks, err.message);
  }
}

const disableACLDialog = document.getElementById('disableACLDialog');
document.getElementById('disableACLCancelBtn').addEventListener('click', () => disableACLDialog.close());
document.getElementById('disableACLConfirmBtn').addEventListener('click', async () => {
  const dataset = state.aclDataset;
  disableACLDialog.close();
  try {
    const res = await api('PATCH', `/api/datasets/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { acltype: 'off' });
    showOpLog(`Disabled ACLs — ${dataset}`, res.tasks, null);
    state.aclData = await api('GET', `/api/acl/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`);
    renderACLDialog();
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
  try {
    const result = await api('POST', `/api/chown/${encodeURIComponent(dataset).replace(/%2F/g, '/')}`,
      { owner, group, recursive });
    showOpLog(`Ownership changed: ${dataset}`, result.tasks, null);
  } catch (err) {
    showOpLog(`Failed to change ownership of ${dataset}`, err.tasks, err.message);
  }
});

// ── SSE client ────────────────────────────────────────────────────────────────
// Maps SSE topic names → state key + render function.
const sseTopicMap = {
  'pool.query':     { key: 'pools',     render: renderPools },
  'dataset.query':  { key: 'datasets',  render: renderDatasets },
  'snapshot.query': { key: 'snapshots', render: renderSnapshots },
  'iostat':         { key: 'iostat',    render: renderIOStat },
  'user.query':     { key: 'users',     render: renderUsers },
  'group.query':    { key: 'groups',    render: renderGroups },
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
