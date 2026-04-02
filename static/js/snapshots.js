import { state, storeSet } from './store.js';
import { api, esc, fmtBytes, fmtDate, showOpLog, showOpLogRunning, toast } from './utils.js';

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

const reZFSName  = /^[a-zA-Z][a-zA-Z0-9_.:-]*(\/[a-zA-Z][a-zA-Z0-9_.:-]*)*$/;
const reSnapLabel = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$/;

document.getElementById('newSnapForm').addEventListener('submit', async e => {
  e.preventDefault();
  const dataset = document.getElementById('snap-dataset').value.trim();
  const snapname = document.getElementById('snap-label').value.trim();
  const recursive = document.getElementById('snap-recursive').checked;
  if (!reZFSName.test(dataset)) {
    toast('Invalid dataset name', 'error');
    return;
  }
  if (!reSnapLabel.test(snapname)) {
    toast('Invalid snapshot label', 'error');
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
