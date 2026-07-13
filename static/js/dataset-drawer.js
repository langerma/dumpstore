import { state, storeSet } from './store.js';
import { api, delegate, esc, fmtBytes, showOpLog, showOpLogRunning, toast } from './utils.js';
import {
  openDeleteDatasetDialog, openRenameDatasetDialog, openRewriteDialog,
  openUserSpaceDialog, openACLDialog, openChownDialog,
  openNFSDialog, openSMBDialog, openISCSIDialog,
} from './dataset-dialogs.js';
import { openAutoSnapDialog } from './pools.js';
import { revealSnapshotGroup } from './snapshots.js';

// ── Dataset detail drawer ─────────────────────────────────────────────────────
// Slide-in panel opened by clicking a dataset row. Read-only sections
// (summary, sharing, permissions, snapshots, danger) re-render on every state
// change; the Properties form renders only on open and after a successful
// Apply, so SSE updates never clobber in-progress edits (#63).

const drawer = document.getElementById('dataset-drawer');
const backdrop = document.getElementById('drawer-backdrop');

let _renderedFor = '';     // dataset the drawer shell is currently built for
let _propsDirty = false;   // user has touched the Properties form
let _origProps = {};       // prop → display value at fetch time (local values only)
let _fetchedProps = null;  // last fetched /api/dataset-props payload

export function openDatasetDrawer(name) {
  storeSet('selectedDataset', name);
}

export function closeDatasetDrawer() {
  storeSet('selectedDataset', '');
}

function isMounted(d) {
  return d.type === 'filesystem' && d.mountpoint !== 'none' && d.mountpoint !== '-';
}

export function renderDatasetDrawer() {
  const name = state.selectedDataset;
  if (!name) {
    drawer.classList.remove('open');
    backdrop.classList.remove('open');
    _renderedFor = '';
    _propsDirty = false;
    return;
  }

  const d = state.datasets.find(x => x.name === name);
  if (!d) {
    // Dataset disappeared (deleted/renamed) — close.
    closeDatasetDrawer();
    return;
  }

  if (_renderedFor !== name) {
    renderShell(d);
    _renderedFor = name;
    _propsDirty = false;
    _fetchedProps = null;
    loadProps(name);
  }

  drawer.classList.add('open');
  backdrop.classList.add('open');

  renderHeader(d);
  renderSummary(d);
  renderSharing(d);
  renderPerms(d);
  renderSnaps(d);
  renderDanger(d);
}

// Shell: stable sub-containers. Only built when the selected dataset changes;
// the Properties section keeps its form state across read-only re-renders.
function renderShell(_d) {
  drawer.innerHTML = `
    <div class="drawer-header" id="drawer-title"></div>
    <div class="drawer-body">
      <div id="drawer-summary"></div>
      <fieldset class="form-section"><legend>Properties</legend>
        <div id="drawer-props"><div class="loading">Loading properties…</div></div>
      </fieldset>
      <fieldset class="form-section" id="drawer-sharing-section"><legend>Sharing</legend>
        <div id="drawer-sharing"></div>
      </fieldset>
      <fieldset class="form-section" id="drawer-perms-section"><legend>Permissions</legend>
        <div id="drawer-perms"></div>
      </fieldset>
      <fieldset class="form-section"><legend>Snapshots</legend>
        <div id="drawer-snaps"></div>
      </fieldset>
      <fieldset class="form-section danger"><legend>Danger zone</legend>
        <div id="drawer-danger"></div>
      </fieldset>
    </div>`;
}

function renderHeader(d) {
  const shortName = d.name.split('/').pop();
  document.getElementById('drawer-title').innerHTML = `
    <div class="drawer-title-text">
      <span class="type-badge type-${esc(d.depth === 0 ? 'pool' : d.type)}">${esc(d.depth === 0 ? 'pool' : d.type)}</span>
      <strong>${esc(shortName)}</strong>
      ${d.depth > 0 ? `<span class="muted drawer-fullname">${esc(d.name)}</span>` : ''}
    </div>
    <button class="drawer-close" data-action="close" title="Close (Esc)">✕</button>`;
}

function renderSummary(d) {
  const quota = d.quota || 0;
  let usageBar = '';
  if (quota > 0) {
    const pct = Math.min(d.used / quota * 100, 100);
    const barClass = pct > 90 ? 'crit' : pct > 75 ? 'warn' : '';
    usageBar = `
      <div class="pool-bar-wrap"><div class="pool-bar ${barClass}" style="width:${pct.toFixed(1)}%"></div></div>
      <div class="muted drawer-usage-text">${fmtBytes(d.used)} of ${fmtBytes(quota)} quota</div>`;
  }
  document.getElementById('drawer-summary').innerHTML = `
    ${usageBar}
    <div class="sysinfo-grid drawer-summary-grid">
      <div class="si-item"><div class="si-label">Used</div><div class="si-value">${fmtBytes(d.used)}</div></div>
      <div class="si-item"><div class="si-label">Avail</div><div class="si-value">${fmtBytes(d.avail)}</div></div>
      <div class="si-item"><div class="si-label">Refer</div><div class="si-value">${fmtBytes(d.refer)}</div></div>
      <div class="si-item"><div class="si-label">Compression</div><div class="si-value">${esc(d.compression)} (${esc(d.compress_ratio)})</div></div>
      <div class="si-item"><div class="si-label">Reservation</div><div class="si-value">${d.reservation ? fmtBytes(d.reservation) : '—'}</div></div>
      <div class="si-item"><div class="si-label">Mountpoint</div><div class="si-value">${d.mountpoint !== 'none' ? esc(d.mountpoint) : '—'}</div></div>
    </div>`;
}

// ── Properties (schema-driven, lazy, clobber-protected) ───────────────────────

function editableProps(d) {
  return (state.schema?.dataset_properties || [])
    .filter(p => p.editable && (p.applies_to || []).includes(d.type));
}

async function loadProps(name) {
  try {
    const encodedName = name.split('/').map(encodeURIComponent).join('/');
    const props = await api('GET', '/api/dataset-props/' + encodedName);
    if (state.selectedDataset !== name) return; // user navigated away mid-fetch
    _fetchedProps = props;
    renderProps();
  } catch (e) {
    const el = document.getElementById('drawer-props');
    if (el) el.innerHTML = `<p class="op-error">Failed to load properties: ${esc(e.message)}</p>`;
  }
}

function renderProps() {
  const el = document.getElementById('drawer-props');
  const d = state.datasets.find(x => x.name === state.selectedDataset);
  if (!el || !d || !_fetchedProps) return;

  _origProps = {};
  const fields = editableProps(d).map(p => {
    const prop = _fetchedProps[p.name] || {};
    const isLocal = prop.source === 'local';
    const display = isLocal ? (prop.value ?? '') : '';
    _origProps[p.name] = display;
    const inherited = !isLocal && prop.value && prop.value !== '-' ? prop.value : '';

    if (p.input_type === 'select') {
      const opts = (p.options || []).map(o => {
        const label = o.value === '' && inherited ? `— inherit (${inherited}) —` : o.label;
        return `<option value="${esc(o.value)}"${o.value === display ? ' selected' : ''}>${esc(label)}</option>`;
      }).join('');
      return `<label>${esc(p.label)}<select data-prop="${esc(p.name)}">${opts}</select></label>`;
    }
    return `<label>${esc(p.label)}
      <input type="text" data-prop="${esc(p.name)}" value="${esc(display)}"
        placeholder="${inherited ? esc(inherited) + ' (inherited)' : 'blank to inherit'}">
    </label>`;
  }).join('');

  const rewriteSupported = state.schema?.capabilities?.rewrite !== false;
  const extras = `
    <div class="row-actions drawer-prop-links">
      ${isMounted(d) ? `<button class="btn-usage btn-small" data-action="usage">Per-user quotas…</button>` : ''}
      ${d.type === 'filesystem' && rewriteSupported ? `<button class="btn-rename btn-small" data-action="rewrite">Rewrite data…</button>` : ''}
    </div>
    ${d.type === 'filesystem' && !rewriteSupported ? '<p class="muted drawer-note">zfs rewrite requires OpenZFS ≥ 2.3 — not supported by the installed version.</p>' : ''}`;

  el.innerHTML = `
    <div class="form-grid">${fields}</div>
    <div class="dialog-actions drawer-props-actions">
      <button class="btn-secondary btn-small" data-action="reset-props">Reset</button>
      <button class="btn-primary btn-small" data-action="apply-props" disabled>Apply</button>
    </div>
    ${extras}`;
  _propsDirty = false;
}

function setPropsDirty() {
  _propsDirty = true;
  const btn = drawer.querySelector('[data-action="apply-props"]');
  if (btn) btn.disabled = false;
}

async function applyProps() {
  const name = state.selectedDataset;
  const body = {};
  drawer.querySelectorAll('#drawer-props [data-prop]').forEach(el => {
    const val = el.tagName === 'INPUT' ? el.value.trim() : el.value;
    if (val !== (_origProps[el.dataset.prop] ?? '')) body[el.dataset.prop] = val;
  });
  if (Object.keys(body).length === 0) {
    toast('No changes to apply', 'ok');
    _propsDirty = false;
    renderProps();
    return;
  }
  showOpLogRunning('Updating properties…');
  try {
    const encodedName = name.split('/').map(encodeURIComponent).join('/');
    const result = await api('PATCH', '/api/datasets/' + encodedName, body);
    showOpLog(`Properties updated: ${name}`, result.tasks, null);
    _propsDirty = false;
    const datasets = await api('GET', '/api/datasets');
    storeSet('datasets', datasets || []);
    if (state.selectedDataset === name) await loadProps(name);
  } catch (e) {
    showOpLog(`Failed to update ${name}`, e.tasks, e.message);
  }
}

// ── Read-only sections ────────────────────────────────────────────────────────

function renderSharing(d) {
  const section = document.getElementById('drawer-sharing-section');
  const el = document.getElementById('drawer-sharing');
  if (d.type === 'volume') {
    section.style.display = '';
    const target = state.iscsiTargets.find(t => t.zvol_name === d.name);
    el.innerHTML = `
      <div class="drawer-row">
        <span class="drawer-row-label">iSCSI</span>
        ${target ? `<code class="drawer-row-value" title="${esc(target.iqn)}">${esc(target.iqn)}</code>` : '<span class="muted drawer-row-value">not exposed</span>'}
        <button class="btn-iscsi btn-small${target ? ' active' : ''}" data-action="iscsi">Configure…</button>
      </div>`;
    return;
  }
  if (!isMounted(d)) {
    section.style.display = 'none';
    return;
  }
  section.style.display = '';
  const nfsOn = d.sharenfs && d.sharenfs !== 'off' && d.sharenfs !== '-';
  const smbShare = state.smbShares.find(s => s.path === d.mountpoint);
  el.innerHTML = `
    <div class="drawer-row">
      <span class="drawer-row-label">NFS</span>
      ${nfsOn ? `<code class="drawer-row-value">${esc(d.sharenfs)}</code>` : '<span class="muted drawer-row-value">not shared</span>'}
      <button class="btn-nfs btn-small${nfsOn ? ' active' : ''}" data-action="nfs">Configure…</button>
    </div>
    <div class="drawer-row">
      <span class="drawer-row-label">SMB</span>
      ${smbShare ? `<code class="drawer-row-value">${esc(smbShare.name)}</code>` : '<span class="muted drawer-row-value">not shared</span>'}
      <button class="btn-smb btn-small${smbShare ? ' active' : ''}" data-action="smb">Configure…</button>
    </div>`;
}

function renderPerms(d) {
  const section = document.getElementById('drawer-perms-section');
  const el = document.getElementById('drawer-perms');
  if (d.type === 'volume') {
    section.style.display = 'none';
    return;
  }
  section.style.display = '';
  const hasACL = !!state.aclStatus[d.name];
  el.innerHTML = `
    <div class="drawer-row">
      <span class="drawer-row-label">ACLs</span>
      <span class="${hasACL ? '' : 'muted '}drawer-row-value">${hasACL ? 'entries configured' : 'none'}</span>
      <button class="btn-acl btn-small${hasACL ? ' active' : ''}" data-action="acl">Manage…</button>
    </div>
    ${isMounted(d) ? `
    <div class="drawer-row">
      <span class="drawer-row-label">Owner</span>
      <span class="muted drawer-row-value">${esc(d.mountpoint)}</span>
      <button class="btn-chown btn-small" data-action="chown">Change…</button>
    </div>` : ''}`;
}

function renderSnaps(d) {
  const count = state.snapshots.filter(s => s.dataset === d.name).length;
  const autosnapOn = state.autoSnapshot[d.name]?.['com.sun:auto-snapshot']?.value === 'true';
  document.getElementById('drawer-snaps').innerHTML = `
    <div class="drawer-row">
      <span class="drawer-row-label">Snapshots</span>
      <span class="drawer-row-value">${count}${autosnapOn ? ' <span class="status-chip badge-green">auto</span>' : ''}</span>
      <button class="btn-autosnap btn-small${autosnapOn ? ' active' : ''}" data-action="autosnap">Schedule…</button>
      ${count > 0 ? `<button class="btn-rename btn-small" data-action="view-snaps">View →</button>` : ''}
    </div>`;
}

function renderDanger(d) {
  const el = document.getElementById('drawer-danger');
  if (d.depth === 0) {
    el.innerHTML = '<p class="muted drawer-note">Pool root — destroy the pool from the Pools tab (zpool destroy).</p>';
    return;
  }
  el.innerHTML = `
    <div class="row-actions">
      <button class="btn-rename btn-small" data-action="rename">Rename…</button>
      <button class="btn-danger btn-small" data-action="del">Delete…</button>
    </div>`;
}

// ── Wiring (bound once at module init) ────────────────────────────────────────

delegate(drawer, {
  close:         () => closeDatasetDrawer(),
  'apply-props': () => applyProps(),
  'reset-props': () => renderProps(),
  usage:         () => openUserSpaceDialog(state.selectedDataset),
  rewrite:       () => openRewriteDialog(state.selectedDataset),
  nfs:           () => openNFSDialog(state.selectedDataset),
  smb:           () => openSMBDialog(state.selectedDataset),
  iscsi:         () => openISCSIDialog(state.selectedDataset),
  acl:           () => openACLDialog(state.selectedDataset),
  chown:         () => openChownDialog(state.selectedDataset),
  autosnap:      () => openAutoSnapDialog(state.selectedDataset),
  rename:        () => openRenameDatasetDialog(state.selectedDataset),
  del:           () => {
    const d = state.datasets.find(x => x.name === state.selectedDataset);
    if (d) openDeleteDatasetDialog(d.name, d.type);
  },
  'view-snaps':  () => {
    const name = state.selectedDataset;
    closeDatasetDrawer();
    document.querySelector('.tab-btn[data-tab="snapshots"]')?.click();
    revealSnapshotGroup(name);
  },
});

// Dirty-tracking for the Properties form.
drawer.addEventListener('input', e => {
  if (e.target.closest('#drawer-props')) setPropsDirty();
});
drawer.addEventListener('change', e => {
  if (e.target.closest('#drawer-props')) setPropsDirty();
});

backdrop.addEventListener('click', () => closeDatasetDrawer());

// Esc closes the drawer — but modal <dialog>s own Esc while open.
document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && state.selectedDataset && !document.querySelector('dialog[open]')) {
    closeDatasetDrawer();
  }
});
