import { state, storeSet } from './store.js';
import { api, esc, fmtBytes, showOpLog, showOpLogRunning, toast, reZFSName } from './utils.js';

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

export function renderDatasets() {
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
            ${canDelete ? `<button class="btn-rename btn-small" data-ds="${esc(d.name)}">Rename</button>` : ''}
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

  wrap.querySelectorAll('.btn-rename[data-ds]').forEach(btn => {
    btn.addEventListener('click', () => openRenameDatasetDialog(btn.dataset.ds));
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
    btn.addEventListener('click', () => window.openAutoSnapDialog(btn.dataset.ds));
  });
}

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

// ── ACL dialog ────────────────────────────────────────────────────────────────
const aclDialog = document.getElementById('aclDialog');
document.getElementById('aclDialogClose').addEventListener('click', () => aclDialog.close());

export async function openACLDialog(dataset) {
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
    if (!principal) { toast('Principal is required', 'err'); return; }

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

    if (!perms) { toast('Select at least one permission', 'err'); return; }

    const ace = `${type}:${flags}:${principal}:${perms}`;
    await addACLEntry(ace, false);
  });
}

export async function refreshACLStatus() {
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

// ── Chown dialog ──────────────────────────────────────────────────────────────
const chownDialog = document.getElementById('chownDialog');
document.getElementById('chownCancelBtn').addEventListener('click', () => chownDialog.close());

let _chownDataset = '';

export async function openChownDialog(dataset) {
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
    toast(`Could not read ownership: ${err.message}`, 'err');
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

export async function openNFSDialog(dataset) {
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

export async function openSMBDialog(dataset) {
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

export async function openISCSIDialog(dataset) {
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

  if (!iqn) { toast('IQN is required', 'err'); return; }

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

// ── Rename Dataset dialog ───────────────────���─────────────────────────────────
const renameDatasetDialog = document.getElementById('renameDatasetDialog');
document.getElementById('renameDatasetCancelBtn').addEventListener('click', () => renameDatasetDialog.close());

function openRenameDatasetDialog(name) {
  document.getElementById('renameDatasetCurrent').value = name;
  document.getElementById('renameDatasetNewName').value = name;
  renameDatasetDialog.showModal();
  // Select just the last path component for easy editing.
  const input = document.getElementById('renameDatasetNewName');
  const lastSlash = name.lastIndexOf('/');
  input.setSelectionRange(lastSlash + 1, name.length);
  input.focus();
}

document.getElementById('renameDatasetForm').addEventListener('submit', async e => {
  e.preventDefault();
  const name = document.getElementById('renameDatasetCurrent').value;
  const newName = document.getElementById('renameDatasetNewName').value.trim();
  if (!reZFSName.test(newName)) {
    toast('Invalid dataset name', 'err');
    return;
  }
  if (name === newName) {
    renameDatasetDialog.close();
    toast('No change', 'ok');
    return;
  }
  renameDatasetDialog.close();
  showOpLogRunning('Renaming dataset…');
  try {
    const result = await api('POST', '/api/datasets/rename', { name, new_name: newName });
    showOpLog(`Renamed: ${name} → ${newName}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    storeSet('datasets', datasets || []);
  } catch (err) {
    showOpLog('Rename failed', err.tasks, err.message);
  }
});
