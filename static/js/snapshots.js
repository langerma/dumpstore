import { state, storeSet } from './store.js';
import { api, delegate, esc, fmtBytes, fmtDate, showOpLog, showOpLogRunning, toast, reZFSName, reSnapLabel } from './utils.js';

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

export function renderSnapshots() {
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
      <td style="width:1.5rem"><input type="checkbox" class="snap-check" data-action="check" data-snap="${esc(s.name)}" ${checked}></td>
      <td class="mono">${esc(s.dataset)}</td>
      <td class="mono">${esc(s.snap_label)}</td>
      <td>${fmtBytes(s.used)}</td>
      <td>${fmtBytes(s.refer)}</td>
      <td class="muted">${fmtDate(s.creation)}</td>
      <td class="muted">${s.clones && s.clones !== '-' ? esc(s.clones) : '—'}</td>
      <td>
        <div class="row-actions">
          <button class="btn-diff btn-small" data-action="diff" data-snap="${esc(s.name)}">Diff</button>
          <button class="btn-clone btn-small" data-action="clone" data-snap="${esc(s.name)}">Clone</button>
          <button class="btn-send btn-small" data-action="send" data-snap="${esc(s.name)}">Send</button>
          <button class="btn-del" data-action="del" data-snap="${esc(s.name)}">Delete</button>
        </div>
      </td>
    </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th style="width:1.5rem"><input type="checkbox" id="snapCheckAll" data-action="check-all" ${allChecked ? 'checked' : ''}></th>
          <th>Dataset</th><th>Snapshot</th><th>Used</th>
          <th>Refer</th><th>Created</th><th>Clones</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;

  _updateMultiDeleteBtn();
}

// One delegated listener per event type on the stable wrapper; survives renders.
const snapshotsWrap = document.getElementById('snapshots-table-wrap');
delegate(snapshotsWrap, {
  diff:  ({ snap }) => openDiffSnapDialog(snap),
  clone: ({ snap }) => openCloneSnapDialog(snap),
  send:  ({ snap }) => openSendSnapDialog(snap),
  del:   ({ snap }) => deleteSnapshot(snap),
});
delegate(snapshotsWrap, {
  check: ({ snap }, el) => {
    if (el.checked) state.selectedSnaps.add(snap);
    else state.selectedSnaps.delete(snap);
    _updateMultiDeleteBtn();
    // sync select-all state
    const all = [...snapshotsWrap.querySelectorAll('.snap-check')];
    document.getElementById('snapCheckAll').checked = all.length > 0 && all.every(c => c.checked);
  },
  'check-all': (d, el) => {
    snapshotsWrap.querySelectorAll('.snap-check').forEach(cb => {
      if (el.checked) state.selectedSnaps.add(cb.dataset.snap);
      else state.selectedSnaps.delete(cb.dataset.snap);
    });
    renderSnapshots();
  },
}, 'change');

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

export function deleteSnapshot(name) {
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
  if (!reZFSName.test(dataset)) {
    toast('Invalid dataset name', 'err');
    return;
  }
  if (!reSnapLabel.test(snapname)) {
    toast('Invalid snapshot label', 'err');
    return;
  }
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

// ── Clone Snapshot dialog ────────────────────────────────────────────────────────
const cloneSnapDialog = document.getElementById('cloneSnapDialog');
document.getElementById('cloneSnapCancelBtn').addEventListener('click', () => cloneSnapDialog.close());

function openCloneSnapDialog(snapshotName) {
  document.getElementById('cloneSnapSource').value = snapshotName;
  // Suggest target: dataset part + '-clone'
  const at = snapshotName.indexOf('@');
  const dsName = at >= 0 ? snapshotName.substring(0, at) : snapshotName;
  document.getElementById('cloneSnapTarget').value = dsName + '-clone';
  cloneSnapDialog.showModal();
  const input = document.getElementById('cloneSnapTarget');
  input.focus();
  input.select();
}

document.getElementById('cloneSnapForm').addEventListener('submit', async e => {
  e.preventDefault();
  const snapshot = document.getElementById('cloneSnapSource').value;
  const target = document.getElementById('cloneSnapTarget').value.trim();
  if (!reZFSName.test(target)) {
    toast('Invalid target dataset name', 'err');
    return;
  }
  cloneSnapDialog.close();
  showOpLogRunning('Cloning snapshot…');
  try {
    const result = await api('POST', '/api/snapshots/clone', { snapshot, target });
    showOpLog(`Cloned: ${snapshot} → ${target}`, result.tasks, null);
    const datasets = await api('GET', '/api/datasets');
    storeSet('datasets', datasets || []);
  } catch (err) {
    showOpLog('Clone failed', err.tasks, err.message);
  }
});

// ── Snapshot Diff dialog ──────────────────────────────────────────────────────
const diffSnapDialog = document.getElementById('diffSnapDialog');
document.getElementById('diffSnapCloseBtn').addEventListener('click', () => diffSnapDialog.close());

let _diffEntries = [];

function openDiffSnapDialog(snapshotName) {
  document.getElementById('diffSnapSource').textContent = snapshotName;
  const at = snapshotName.indexOf('@');
  const dsName = at >= 0 ? snapshotName.substring(0, at) : snapshotName;
  const fromSnap = state.snapshots.find(s => s.name === snapshotName);

  // Comparison targets: the live filesystem, or any later snapshot of the
  // same dataset (zfs diff requires from to be the older snapshot).
  const sel = document.getElementById('diffSnapTo');
  sel.innerHTML = '<option value="">current filesystem state</option>';
  state.snapshots
    .filter(s => s.dataset === dsName && s.name !== snapshotName
              && (s.creation || 0) >= (fromSnap?.creation || 0))
    .sort((a, b) => (a.creation || 0) - (b.creation || 0))
    .forEach(s => {
      const opt = document.createElement('option');
      opt.value = s.name;
      opt.textContent = s.snap_label;
      sel.appendChild(opt);
    });

  _diffEntries = [];
  document.getElementById('diffSnapFilter').value = '';
  document.getElementById('diffSnapCount').textContent = '';
  document.getElementById('diffSnapResults').innerHTML =
    '<div class="loading">Choose a comparison target and run the diff.</div>';
  diffSnapDialog.showModal();
}

const _diffChangeMeta = {
  '+': { cls: 'diff-added',    label: '+' },
  '-': { cls: 'diff-removed',  label: '−' },
  'M': { cls: 'diff-modified', label: 'M' },
  'R': { cls: 'diff-renamed',  label: 'R' },
};

// Cap rendered rows — the full result stays in _diffEntries for filtering.
const DIFF_RENDER_LIMIT = 2000;

function renderDiffResults() {
  const wrap = document.getElementById('diffSnapResults');
  const q = document.getElementById('diffSnapFilter').value.toLowerCase();
  const items = q
    ? _diffEntries.filter(e => e.path.toLowerCase().includes(q) || (e.new_path || '').toLowerCase().includes(q))
    : _diffEntries;
  if (!items.length) {
    wrap.innerHTML = '<div class="loading">No changes.</div>';
    return;
  }
  const overflow = items.length > DIFF_RENDER_LIMIT
    ? `<div class="loading">… ${items.length - DIFF_RENDER_LIMIT} more — refine the filter</div>`
    : '';
  wrap.innerHTML = items.slice(0, DIFF_RENDER_LIMIT).map(e => {
    const meta = _diffChangeMeta[e.change] || { cls: '', label: e.change };
    const path = e.new_path ? `${esc(e.path)} → ${esc(e.new_path)}` : esc(e.path);
    return `<div class="diff-line ${meta.cls}"><span class="diff-change">${esc(meta.label)}</span><span>${path}</span></div>`;
  }).join('') + overflow;
}

document.getElementById('diffSnapFilter').addEventListener('input', renderDiffResults);

document.getElementById('diffSnapRunBtn').addEventListener('click', async () => {
  const from = document.getElementById('diffSnapSource').textContent;
  const to = document.getElementById('diffSnapTo').value;
  const wrap = document.getElementById('diffSnapResults');
  wrap.innerHTML = '<div class="loading">Running zfs diff…</div>';
  document.getElementById('diffSnapCount').textContent = '';
  try {
    const data = await api('GET', `/api/snapshots/diff?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`);
    _diffEntries = data.entries || [];
    renderDiffResults();
    const note = data.truncated ? ' (truncated)' : '';
    document.getElementById('diffSnapCount').textContent =
      `${_diffEntries.length} change${_diffEntries.length === 1 ? '' : 's'}${note}`;
  } catch (err) {
    wrap.innerHTML = '<div class="loading">Diff failed.</div>';
    toast(`Diff failed: ${err.message}`, 'err');
  }
});

// ── Send Snapshot dialog ──────────────────────────────────────────────────────
const sendSnapDialog = document.getElementById('sendSnapDialog');
document.getElementById('sendSnapCancelBtn').addEventListener('click', () => sendSnapDialog.close());

const reRemoteSpec = /^[a-zA-Z_][a-zA-Z0-9._-]*@[a-zA-Z0-9.-]+$/;

function openSendSnapDialog(snapshotName) {
  document.getElementById('sendSnapSource').value = snapshotName;
  const at = snapshotName.indexOf('@');
  const dsName = at >= 0 ? snapshotName.substring(0, at) : snapshotName;

  // Suggest target: same dataset path under "backup/"
  const lastSlash = dsName.lastIndexOf('/');
  const tail = lastSlash >= 0 ? dsName.substring(lastSlash + 1) : dsName;
  document.getElementById('sendSnapTarget').value = 'backup/' + tail;
  document.getElementById('sendSnapRemote').value = '';
  document.getElementById('sendSnapRaw').checked = false;

  // Populate "incremental from" with other snapshots of the same dataset,
  // older than the selected one.
  const sel = document.getElementById('sendSnapIncremental');
  sel.innerHTML = '<option value="">— full send —</option>';
  const candidates = state.snapshots
    .filter(s => s.dataset === dsName && s.name !== snapshotName)
    .sort((a, b) => (b.creation || 0) - (a.creation || 0));
  for (const s of candidates) {
    const opt = document.createElement('option');
    opt.value = s.name;
    opt.textContent = s.snap_label;
    sel.appendChild(opt);
  }

  sendSnapDialog.showModal();
  document.getElementById('sendSnapTarget').focus();
}

document.getElementById('sendSnapForm').addEventListener('submit', async e => {
  e.preventDefault();
  const snapshot = document.getElementById('sendSnapSource').value;
  const target = document.getElementById('sendSnapTarget').value.trim();
  const incremental_from = document.getElementById('sendSnapIncremental').value;
  const remote = document.getElementById('sendSnapRemote').value.trim();
  const raw = document.getElementById('sendSnapRaw').checked;
  if (!reZFSName.test(target) || !target.includes('/')) {
    toast('Invalid target dataset name', 'err');
    return;
  }
  if (remote && !reRemoteSpec.test(remote)) {
    toast('Invalid remote (expected user@host)', 'err');
    return;
  }
  sendSnapDialog.close();
  try {
    const body = { snapshot, target, raw };
    if (incremental_from) body.incremental_from = incremental_from;
    if (remote) body.remote = remote;
    await api('POST', '/api/snapshots/send', body);
    toast('Replication started — see Jobs tab', 'ok');
  } catch (err) {
    toast(`Send failed to start: ${err.message}`, 'err');
  }
});
