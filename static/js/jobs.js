import { state, storeSet } from './store.js';
import { api, esc, fmtDate, toast } from './utils.js';

const STATUS_BADGES = {
  pending:     'badge-blue',
  running:     'badge-blue',
  success:     'badge-green',
  failed:      'badge-red',
  cancelled:   'badge-yellow',
  interrupted: 'badge-yellow',
};

function fmtDuration(startedAt, finishedAt) {
  if (!startedAt) return '—';
  const start = new Date(startedAt).getTime();
  const end = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  let secs = Math.max(0, Math.floor((end - start) / 1000));
  const h = Math.floor(secs / 3600); secs -= h * 3600;
  const m = Math.floor(secs / 60); secs -= m * 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${secs}s`;
  return `${secs}s`;
}

function jobTarget(job) {
  // RunPipeline records argv as left + ["|"] + right. For snapshot.send:
  //   left  = zfs send [--raw] [-i prev] <snapshot>
  //   right = zfs recv <target>
  //         | ssh -o BatchMode=yes <remote> zfs recv <target>
  if (job.type === 'snapshot.send' && Array.isArray(job.args)) {
    const pipeIdx = job.args.indexOf('|');
    if (pipeIdx > 0) {
      const left = job.args.slice(0, pipeIdx);
      const right = job.args.slice(pipeIdx + 1);
      const src = left[left.length - 1] || '?';
      let dst = '?';
      let remote = '';
      if (right[0] === 'ssh') {
        // ["ssh","-o","BatchMode=yes","user@host","zfs","recv","target"]
        remote = right[3] + ':';
        dst = right[right.length - 1];
      } else {
        // ["zfs","recv","target"]
        dst = right[right.length - 1];
      }
      return `${src} → ${remote}${dst}`;
    }
  }
  return job.type;
}

export function renderJobs() {
  const wrap = document.getElementById('jobs-wrap');
  if (!wrap) return;
  const jobs = state.jobs || [];
  if (!jobs.length) {
    wrap.innerHTML = '<div class="loading">No jobs yet.</div>';
    return;
  }
  const rows = jobs.map(j => {
    const badge = STATUS_BADGES[j.status] || 'badge-blue';
    const isRunning = j.status === 'running' || j.status === 'pending';
    const actionBtn = isRunning
      ? `<button class="btn-cancel-job btn-small" data-id="${esc(j.id)}">Cancel</button>`
      : `<button class="btn-remove-job btn-small" data-id="${esc(j.id)}">Remove</button>`;
    return `<tr>
      <td><span class="health-badge ${badge}">${esc(j.status)}</span></td>
      <td class="mono">${esc(j.type)}</td>
      <td class="mono">${esc(jobTarget(j))}</td>
      <td class="muted">${fmtDate(new Date(j.started_at).getTime() / 1000)}</td>
      <td>${fmtDuration(j.started_at, j.finished_at)}</td>
      <td>
        <div class="row-actions">
          <button class="btn-job-detail btn-small" data-id="${esc(j.id)}">Details</button>
          ${actionBtn}
        </div>
      </td>
    </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Status</th><th>Type</th><th>Target</th>
          <th>Started</th><th>Runtime</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;

  wrap.querySelectorAll('.btn-cancel-job').forEach(btn => {
    btn.addEventListener('click', () => openCancelJobDialog(btn.dataset.id));
  });
  wrap.querySelectorAll('.btn-remove-job').forEach(btn => {
    btn.addEventListener('click', () => removeJob(btn.dataset.id));
  });
  wrap.querySelectorAll('.btn-job-detail').forEach(btn => {
    btn.addEventListener('click', () => openJobDetailDialog(btn.dataset.id));
  });
}

async function removeJob(id) {
  try {
    await api('DELETE', `/api/jobs/${encodeURIComponent(id)}`);
    storeSet('jobs', (state.jobs || []).filter(j => j.id !== id));
  } catch (err) {
    toast(`Remove failed: ${err.message}`, 'err');
  }
}

// ── Cancel dialog ─────────────────────────────────────────────────────────────
const cancelDialog = document.getElementById('cancelJobDialog');
let _cancelId = '';
document.getElementById('cancelJobBackBtn').addEventListener('click', () => cancelDialog.close());
document.getElementById('cancelJobConfirmBtn').addEventListener('click', async () => {
  const id = _cancelId;
  cancelDialog.close();
  try {
    await api('POST', `/api/jobs/${encodeURIComponent(id)}/cancel`);
    toast('Cancellation signalled', 'ok');
  } catch (err) {
    toast(`Cancel failed: ${err.message}`, 'err');
  }
});

function openCancelJobDialog(id) {
  _cancelId = id;
  const job = (state.jobs || []).find(j => j.id === id);
  document.getElementById('cancelJobDisplayName').textContent = job ? jobTarget(job) : id;
  cancelDialog.showModal();
}

// ── Detail dialog ─────────────────────────────────────────────────────────────
const detailDialog = document.getElementById('jobDetailDialog');
document.getElementById('jobDetailCloseBtn').addEventListener('click', () => detailDialog.close());

function openJobDetailDialog(id) {
  const job = (state.jobs || []).find(j => j.id === id);
  if (!job) {
    toast('Job not found', 'err');
    return;
  }
  document.getElementById('jobDetailTitle').textContent = `${job.type} — ${job.status}`;
  const body = document.getElementById('jobDetailBody');
  const meta = `
    <table class="data-table" style="margin-bottom:0.75rem">
      <tbody>
        <tr><td class="muted">ID</td><td class="mono">${esc(job.id)}</td></tr>
        <tr><td class="muted">Status</td><td>${esc(job.status)}</td></tr>
        <tr><td class="muted">Started</td><td>${esc(new Date(job.started_at).toLocaleString())}</td></tr>
        ${job.finished_at ? `<tr><td class="muted">Finished</td><td>${esc(new Date(job.finished_at).toLocaleString())}</td></tr>` : ''}
        <tr><td class="muted">Runtime</td><td>${fmtDuration(job.started_at, job.finished_at)}</td></tr>
        ${job.exit_code !== undefined && job.exit_code !== 0 ? `<tr><td class="muted">Exit code</td><td>${esc(String(job.exit_code))}</td></tr>` : ''}
        ${job.error ? `<tr><td class="muted">Error</td><td>${esc(job.error)}</td></tr>` : ''}
        <tr><td class="muted">Command</td><td class="mono" style="word-break:break-all">${esc((job.args || []).join(' '))}</td></tr>
      </tbody>
    </table>`;
  const stdout = job.stdout
    ? `<h4 style="margin:0.5rem 0 0.25rem">stdout (last 64 KiB)</h4><pre class="oplog-output">${esc(job.stdout)}</pre>`
    : '';
  const stderr = job.stderr
    ? `<h4 style="margin:0.5rem 0 0.25rem">stderr (last 64 KiB)</h4><pre class="oplog-output">${esc(job.stderr)}</pre>`
    : '';
  body.innerHTML = meta + stdout + stderr;
  detailDialog.showModal();
}

// ── SSE merge helper ──────────────────────────────────────────────────────────
// Replace-by-id, prepend if new. Used by loader.js when handling jobs.update.
export function mergeJob(updated) {
  const list = state.jobs || [];
  const idx = list.findIndex(j => j.id === updated.id);
  let next;
  if (idx >= 0) {
    next = [...list];
    next[idx] = updated;
  } else {
    next = [updated, ...list];
  }
  storeSet('jobs', next);
}
