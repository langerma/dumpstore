// ── XSS helper ────────────────────────────────────────────────────────────────
export function esc(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ── API helpers ───────────────────────────────────────────────────────────────
export async function api(method, path, body) {
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
export function fmtBytes(n) {
  if (n === 0) return '—';
  const units = ['B', 'K', 'M', 'G', 'T', 'P'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return v.toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
}

export function fmtDate(epoch) {
  if (!epoch) return '—';
  return new Date(epoch * 1000).toLocaleString();
}

export function fmtPct(p) {
  return p.toFixed(1) + '%';
}

export function fmtNum(n) {
  if (!n) return '—';
  return n.toLocaleString(undefined, { maximumFractionDigits: 0 });
}

export function fmtHours(h) {
  if (!h) return '—';
  if (h >= 8760) return (h / 8760).toFixed(1) + ' y';
  return h.toLocaleString() + ' h';
}

export function fmtUptime(secs) {
  const s = Math.round(secs);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// ── Operation log dialog ──────────────────────────────────────────────────────
export const opLogDialog = document.getElementById('opLogDialog');
document.getElementById('opLogClose').addEventListener('click', () => opLogDialog.close());

const stepIcons = { ok: '✓', changed: '●', failed: '✗', skipped: '–' };

export function showOpLogRunning(title) {
  document.getElementById('opLogTitle').textContent = title;
  document.getElementById('opLogSteps').innerHTML = `
    <div class="op-step running">
      <span class="op-step-icon">⟳</span>
      <span class="op-step-name">Running…</span>
    </div>`;
  document.getElementById('opLogError').style.display = 'none';
  document.getElementById('opLogClose').disabled = true;
  if (!opLogDialog.open) opLogDialog.showModal();
}

export function showOpLog(title, tasks, errorMsg) {
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
  document.getElementById('opLogClose').disabled = false;
  if (!opLogDialog.open) opLogDialog.showModal();
}

// Append a single task step to the op-log while it is in running state.
// Removes the "Running…" placeholder on the first call.
export function appendOpLogStep(step) {
  const stepsEl = document.getElementById('opLogSteps');
  // Remove the "Running…" placeholder if present
  const running = stepsEl.querySelector('.op-step.running');
  if (running) running.remove();
  const div = document.createElement('div');
  div.className = `op-step ${esc(step.status)}`;
  div.innerHTML = `
    <span class="op-step-icon">${esc(stepIcons[step.status] || '?')}</span>
    <span class="op-step-name">${esc(step.name)}</span>
    ${step.msg ? `<span class="op-step-msg">${esc(step.msg)}</span>` : ''}`;
  stepsEl.appendChild(div);
}

// ── Toast ─────────────────────────────────────────────────────────────────────
let toastTimer;
export function toast(msg, type = 'ok') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = `toast show ${type}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = 'toast'; }, 3500);
}

// ── ZFS name validation regexes ───────────────────────────────────────────────
export const reZFSName   = /^[a-zA-Z][a-zA-Z0-9_.:-]*(\/[a-zA-Z][a-zA-Z0-9_.:-]*)*$/;
export const reSnapLabel = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$/;

// ── Refresh ───────────────────────────────────────────────────────────────────
export function setRefreshing(v) {
  document.getElementById('refreshBtn').classList.toggle('spinning', v);
}
