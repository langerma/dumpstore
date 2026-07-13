import { state, storeSet } from './store.js';
import { api, delegate, esc, fmtBytes, showOpLog, showOpLogRunning, toast, reZFSName } from './utils.js';
import { openDatasetDrawer } from './dataset-drawer.js';
import {
  openDeleteDatasetDialog, openRenameDatasetDialog,
  openNFSDialog, openSMBDialog, openISCSIDialog,
} from './dataset-dialogs.js';
import { openAutoSnapDialog } from './pools.js';

// ── Render: Datasets ──────────────────────────────────────────────────────────
let datasetFilter = '';
document.getElementById('dataset-filter').addEventListener('input', e => {
  datasetFilter = e.target.value.toLowerCase();
  renderDatasets();
});

// Returns true if the row should be hidden because a collapsed ancestor contains it.
function isHiddenByCollapse(name) {
  const parts = name.split('/');
  for (let i = 1; i < parts.length; i++) {
    if (state.collapsedDatasets.has(parts.slice(0, i).join('/'))) return true;
  }
  return false;
}

function isMounted(d) {
  return d.type === 'filesystem' && d.mountpoint !== 'none' && d.mountpoint !== '-';
}

function barClassOf(pct) {
  return pct > 90 ? 'crit' : pct > 75 ? 'warn' : '';
}

// Used cell: mini usage bar against quota when one is set, plain bytes otherwise.
function usedCell(d) {
  if (!d.quota) return fmtBytes(d.used);
  const pct = Math.min(d.used / d.quota * 100, 100);
  return `
    <div class="usage-bar-wrap pool-bar-wrap"><div class="pool-bar ${barClassOf(pct)}" style="width:${pct.toFixed(1)}%"></div></div>
    <span class="usage-text">${fmtBytes(d.used)} / ${fmtBytes(d.quota)}</span>`;
}

// Passive status chips — rendered only when the feature is active.
function statusChips(d) {
  const chips = [];
  if (d.sharenfs && d.sharenfs !== 'off' && d.sharenfs !== '-') {
    chips.push(`<span class="status-chip badge-blue" title="NFS shared: ${esc(d.sharenfs)}">NFS</span>`);
  }
  const smbShare = isMounted(d) ? state.smbShares.find(s => s.path === d.mountpoint) : null;
  if (smbShare) chips.push(`<span class="status-chip badge-blue" title="SMB shared: ${esc(smbShare.name)}">SMB</span>`);
  const iscsiTarget = d.type === 'volume' ? state.iscsiTargets.find(t => t.zvol_name === d.name) : null;
  if (iscsiTarget) chips.push(`<span class="status-chip badge-blue" title="iSCSI target: ${esc(iscsiTarget.iqn)}">iSCSI</span>`);
  if (state.autoSnapshot[d.name]?.['com.sun:auto-snapshot']?.value === 'true') {
    chips.push('<span class="status-chip badge-green" title="Auto-snapshot enabled">snap</span>');
  }
  if (state.aclStatus[d.name]) chips.push('<span class="status-chip badge-yellow" title="ACL entries configured">acl</span>');
  return chips.join('');
}

// Quick actions, revealed on row hover/focus. Everything here (and more) is
// also reachable through the detail drawer — nothing is hover-only.
function rowActions(d) {
  const canDelete = d.depth > 0;
  return `
    <div class="row-actions">
      ${isMounted(d) ? `<button class="btn-nfs btn-small" data-action="nfs" data-ds="${esc(d.name)}">NFS</button>` : ''}
      ${isMounted(d) ? `<button class="btn-smb btn-small" data-action="smb" data-ds="${esc(d.name)}">SMB</button>` : ''}
      ${d.type === 'volume' ? `<button class="btn-iscsi btn-small" data-action="iscsi" data-ds="${esc(d.name)}">iSCSI</button>` : ''}
      <button class="btn-autosnap btn-small" data-action="autosnap" data-ds="${esc(d.name)}" title="Auto-snapshot schedule">Snap</button>
      ${canDelete ? `<button class="btn-rename btn-small" data-action="rename" data-ds="${esc(d.name)}">Rename</button>` : ''}
      ${canDelete ? `<button class="btn-del" data-action="del" data-ds="${esc(d.name)}" data-type="${esc(d.type)}">Delete</button>` : ''}
    </div>`;
}

function collapseToggle(d, childCount, filtering) {
  if (filtering || childCount === 0) return '<span class="tree-spacer"></span>';
  const collapsed = state.collapsedDatasets.has(d.name);
  const icon = collapsed ? '▶' : '▼';
  const title = collapsed ? `Expand (${childCount} hidden)` : 'Collapse';
  return `<button class="tree-toggle" data-action="collapse" data-ds="${esc(d.name)}" title="${title}">${icon}</button>`;
}

function datasetRow(d, all, filtering) {
  const shortName = d.name.split('/').pop();
  const childCount = all.filter(c => c.name.startsWith(d.name + '/')).length;
  const selected = d.name === state.selectedDataset ? ' selected' : '';
  return `
    <tr class="ds-row${selected}" data-action="open" data-ds="${esc(d.name)}">
      <td class="dataset-indent" style="--depth:${d.depth}">${collapseToggle(d, childCount, filtering)}${typeBadge(d.type)} ${esc(shortName)}</td>
      <td>${usedCell(d)}</td>
      <td>${fmtBytes(d.avail)}</td>
      <td>${fmtBytes(d.refer)}</td>
      <td class="muted">${d.mountpoint !== 'none' ? esc(d.mountpoint) : '—'}</td>
      <td>${statusChips(d)}</td>
      <td>${rowActions(d)}</td>
    </tr>`;
}

// Pool header row — the pool's root dataset merged with pool health/capacity.
function poolHeaderRow(root, all, filtering) {
  const pool = state.pools.find(p => p.name === root.pool);
  const health = pool
    ? `<span class="health-badge health-${esc(pool.health)}">${esc(pool.health)}</span>`
    : '';
  let capacity = fmtBytes(root.used);
  if (pool) {
    const pct = Math.min(pool.used_percent, 100);
    capacity = `
      <div class="usage-bar-wrap pool-bar-wrap"><div class="pool-bar ${barClassOf(pct)}" style="width:${pct.toFixed(1)}%"></div></div>
      <span class="usage-text">${fmtBytes(pool.alloc)} / ${fmtBytes(pool.size)}</span>`;
  }
  const childCount = all.filter(c => c.name.startsWith(root.name + '/')).length;
  const selected = root.name === state.selectedDataset ? ' selected' : '';
  return `
    <tr class="pool-header-row${selected}" data-action="open" data-ds="${esc(root.name)}">
      <td class="dataset-indent" style="--depth:0">${collapseToggle(root, childCount, filtering)}<span class="pool-name">${esc(root.name)}</span> ${health}</td>
      <td>${capacity}</td>
      <td>${fmtBytes(root.avail)}</td>
      <td>${fmtBytes(root.refer)}</td>
      <td class="muted">${root.mountpoint !== 'none' ? esc(root.mountpoint) : '—'}</td>
      <td>${statusChips(root)}</td>
      <td>${rowActions(root)}</td>
    </tr>`;
}

export function renderDatasets() {
  const wrap = document.getElementById('datasets-table-wrap');
  const all = state.datasets;

  // Apply text filter — when filtering, disable collapse logic for clarity.
  const filtering = datasetFilter.length > 0;
  const items = filtering
    ? all.filter(d => d.name.toLowerCase().includes(datasetFilter))
    : all.filter(d => !isHiddenByCollapse(d.name));

  if (!items.length) {
    wrap.innerHTML = '<div class="loading">No datasets found.</div>';
    return;
  }

  // Group into pool sections. The pool header always renders for a section
  // that has any visible row, so hierarchy context survives filtering.
  const sections = [];
  const seenPools = [];
  for (const d of items) {
    if (!seenPools.includes(d.pool)) seenPools.push(d.pool);
  }
  for (const poolName of seenPools) {
    const root = all.find(d => d.name === poolName) || items.find(d => d.pool === poolName);
    const children = items.filter(d => d.pool === poolName && d.name !== root.name);
    sections.push(`
      <tbody class="pool-section" data-pool="${esc(poolName)}">
        ${poolHeaderRow(root, all, filtering)}
        ${children.map(d => datasetRow(d, all, filtering)).join('')}
      </tbody>`);
  }

  wrap.innerHTML = `
    <div class="table-wrap">
      <table class="hover-actions">
        <thead><tr>
          <th>Name</th><th>Used</th><th>Avail</th>
          <th>Refer</th><th>Mount</th><th>Status</th><th></th>
        </tr></thead>
        ${sections.join('')}
      </table>
    </div>`;
}

// One delegated listener on the stable wrapper; survives every innerHTML render.
delegate(document.getElementById('datasets-table-wrap'), {
  collapse: ({ ds }) => {
    if (state.collapsedDatasets.has(ds)) state.collapsedDatasets.delete(ds);
    else state.collapsedDatasets.add(ds);
    renderDatasets();
  },
  open: ({ ds }) => {
    // Don't hijack text selection (e.g. copying a mountpoint).
    if (window.getSelection()?.toString()) return;
    openDatasetDrawer(ds);
  },
  rename:   ({ ds }) => openRenameDatasetDialog(ds),
  del:      ({ ds, type }) => openDeleteDatasetDialog(ds, type),
  nfs:      ({ ds }) => openNFSDialog(ds),
  smb:      ({ ds }) => openSMBDialog(ds),
  iscsi:    ({ ds }) => openISCSIDialog(ds),
  autosnap: ({ ds }) => openAutoSnapDialog(ds),
});

function typeBadge(type) {
  return `<span class="type-badge type-${esc(type)}">${esc(type)}</span>`;
}

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
  if (!reZFSName.test(body.name)) {
    toast('Invalid dataset name', 'err');
    return;
  }
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
