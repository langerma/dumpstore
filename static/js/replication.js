import { state, storeSet } from './store.js';
import { api, delegate, esc, toast } from './utils.js';

const STATUS_BADGES = {
  success:     'badge-green',
  failed:      'badge-red',
  cancelled:   'badge-yellow',
  interrupted: 'badge-yellow',
  running:     'badge-blue',
  pending:     'badge-blue',
};

function lastRunFor(task) {
  const runs = task.last_runs || [];
  return runs.length ? runs[runs.length - 1] : null;
}

function fmtDateISO(s) {
  if (!s) return '—';
  return new Date(s).toLocaleString();
}

export function renderReplications() {
  const wrap = document.getElementById('replication-wrap');
  if (!wrap) return;
  const tasks = state.replication || [];
  if (!tasks.length) {
    wrap.innerHTML = '<div class="loading">No replication tasks. Click <em>New replication task</em> to add one.</div>';
    return;
  }
  const rows = tasks.map(t => {
    const last = lastRunFor(t);
    const badge = last ? `<span class="health-badge ${STATUS_BADGES[last.status] || 'badge-blue'}">${esc(last.status)}</span>` : '<span class="muted">never</span>';
    const lastWhen = last ? fmtDateISO(last.finished_at || last.started_at) : '—';
    const path = `${esc(t.source)} → ${t.remote ? esc(t.remote) + ':' : ''}${esc(t.target)}`;
    const enabledPill = t.enabled
      ? '<span class="health-badge badge-green">enabled</span>'
      : '<span class="health-badge badge-yellow">disabled</span>';
    return `<tr>
      <td>${esc(t.name)}</td>
      <td class="mono">${path}</td>
      <td class="mono">${esc(t.schedule)}</td>
      <td>${enabledPill}</td>
      <td>${badge}</td>
      <td class="muted">${lastWhen}</td>
      <td>
        <div class="row-actions">
          <button class="btn-rename btn-small btn-repl-run" data-action="run" data-id="${esc(t.id)}">Run now</button>
          <button class="btn-rename btn-small btn-repl-history" data-action="history" data-id="${esc(t.id)}">History</button>
          <button class="btn-rename btn-small btn-repl-edit" data-action="edit" data-id="${esc(t.id)}">Edit</button>
          <button class="btn-del btn-repl-delete" data-action="del" data-id="${esc(t.id)}">Delete</button>
        </div>
      </td>
    </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Name</th><th>Source → Target</th><th>Schedule</th>
          <th>State</th><th>Last run</th><th>When</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
}

// One delegated listener on the stable wrapper; survives renders.
delegate(document.getElementById('replication-wrap'), {
  run:     ({ id }) => runReplication(id),
  history: ({ id }) => openHistoryDialog(id),
  edit:    ({ id }) => openReplDialog(id),
  del:     ({ id }) => openDeleteDialog(id),
});

// ── Create / edit dialog ─────────────────────────────────────────────────────
const replDialog = document.getElementById('replDialog');
const replForm = document.getElementById('replForm');
document.getElementById('newReplBtn').addEventListener('click', () => openReplDialog(''));
document.getElementById('replCancelBtn').addEventListener('click', () => replDialog.close());

function openReplDialog(id) {
  const f = id => document.getElementById(id);
  f('repl-id').value = id || '';
  if (id) {
    const t = (state.replication || []).find(x => x.id === id);
    if (!t) { toast('Task not found', 'err'); return; }
    document.getElementById('replDialogTitle').textContent = 'Edit replication task';
    f('repl-name').value = t.name;
    f('repl-source').value = t.source;
    f('repl-target').value = t.target;
    f('repl-remote').value = t.remote || '';
    f('repl-schedule').value = t.schedule;
    f('repl-retention').value = t.retention_count;
    f('repl-raw').checked = !!t.raw;
    f('repl-recursive').checked = !!t.recursive;
    f('repl-enabled').checked = !!t.enabled;
  } else {
    document.getElementById('replDialogTitle').textContent = 'New replication task';
    replForm.reset();
    f('repl-enabled').checked = true;
    f('repl-retention').value = 7;
  }
  replDialog.showModal();
}

replForm.addEventListener('submit', async e => {
  e.preventDefault();
  const id = document.getElementById('repl-id').value;
  const body = {
    name: document.getElementById('repl-name').value.trim(),
    source: document.getElementById('repl-source').value.trim(),
    target: document.getElementById('repl-target').value.trim(),
    remote: document.getElementById('repl-remote').value.trim(),
    schedule: document.getElementById('repl-schedule').value.trim(),
    retention_count: parseInt(document.getElementById('repl-retention').value, 10),
    raw: document.getElementById('repl-raw').checked,
    recursive: document.getElementById('repl-recursive').checked,
    enabled: document.getElementById('repl-enabled').checked,
  };
  try {
    if (id) {
      await api('PATCH', `/api/replication/${encodeURIComponent(id)}`, body);
      toast('Task updated', 'ok');
    } else {
      await api('POST', '/api/replication', body);
      toast('Task created', 'ok');
    }
    replDialog.close();
    await refreshReplications();
  } catch (err) {
    toast(`Save failed: ${err.message}`, 'err');
  }
});

// ── Delete dialog ────────────────────────────────────────────────────────────
const replDeleteDialog = document.getElementById('replDeleteDialog');
let _deleteId = '';
document.getElementById('replDeleteCancelBtn').addEventListener('click', () => replDeleteDialog.close());
document.getElementById('replDeleteConfirmBtn').addEventListener('click', async () => {
  const id = _deleteId;
  replDeleteDialog.close();
  try {
    await api('DELETE', `/api/replication/${encodeURIComponent(id)}`);
    toast('Task deleted', 'ok');
    await refreshReplications();
  } catch (err) {
    toast(`Delete failed: ${err.message}`, 'err');
  }
});

function openDeleteDialog(id) {
  _deleteId = id;
  const t = (state.replication || []).find(x => x.id === id);
  document.getElementById('replDeleteName').textContent = t ? t.name : id;
  replDeleteDialog.showModal();
}

// ── History dialog ───────────────────────────────────────────────────────────
const replHistoryDialog = document.getElementById('replHistoryDialog');
document.getElementById('replHistoryCloseBtn').addEventListener('click', () => replHistoryDialog.close());

async function openHistoryDialog(id) {
  const t = (state.replication || []).find(x => x.id === id);
  document.getElementById('replHistoryTitle').textContent = `Run history — ${t ? t.name : id}`;
  const body = document.getElementById('replHistoryBody');
  body.innerHTML = '<div class="loading">Loading…</div>';
  replHistoryDialog.showModal();
  try {
    const runs = await api('GET', `/api/replication/${encodeURIComponent(id)}/history`);
    if (!runs.length) {
      body.innerHTML = '<p class="muted">No runs recorded yet.</p>';
      return;
    }
    const rows = runs.slice().reverse().map(r => `
      <tr>
        <td><span class="health-badge ${STATUS_BADGES[r.status] || 'badge-blue'}">${esc(r.status)}</span></td>
        <td class="muted">${fmtDateISO(r.started_at)}</td>
        <td class="muted">${fmtDateISO(r.finished_at)}</td>
        <td class="mono">${esc(r.snapshot || '—')}</td>
        <td class="mono">${esc(r.job_id || '—')}</td>
        <td>${r.error ? esc(r.error) : ''}</td>
      </tr>`).join('');
    body.innerHTML = `
      <div class="table-wrap">
        <table>
          <thead><tr>
            <th>Status</th><th>Started</th><th>Finished</th>
            <th>Snapshot</th><th>Job ID</th><th>Error</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;
  } catch (err) {
    body.innerHTML = `<p class="op-error">Failed to load: ${esc(err.message)}</p>`;
  }
}

// ── Run now ──────────────────────────────────────────────────────────────────
async function runReplication(id) {
  try {
    const out = await api('POST', `/api/replication/${encodeURIComponent(id)}/run`);
    toast(`Started job ${out.job_id.substring(0, 8)}…`, 'ok');
  } catch (err) {
    toast(`Run failed: ${err.message}`, 'err');
  }
}

// ── Refresh helper ───────────────────────────────────────────────────────────
export async function refreshReplications() {
  try {
    const tasks = await api('GET', '/api/replication');
    storeSet('replication', tasks || []);
  } catch (err) {
    // SSE will retry; surface only on explicit user-driven refresh.
    console.warn('refreshReplications failed', err);
  }
}
