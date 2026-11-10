// ── XSS helper ────────────────────────────────────────────────────────────────
export function esc(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ── API helpers ───────────────────────────────────────────────────────────────
// opId, when given, is sent as X-Operation-ID so the write's progress steps
// can be attributed to it (#184). A fetch-level failure (connection lost)
// is rethrown with err.network = true so callers can recover the result.
// v1`/datasets/${name}/acl` → "/api/v1/datasets/tank%2Fdata/acl".
// Prefixes the API version and percent-encodes EVERY interpolated value, so a
// dataset, snapshot or device name containing '/' or '@' always stays a
// single path segment (#181). Build query strings in the template's literal
// part (`?kind=${kind}`), never by interpolating a pre-built "?a=b" string.
export function v1(strings, ...values) {
  let out = '/api/v1';
  strings.forEach((s, i) => {
    out += s;
    if (i < values.length) out += encodeURIComponent(values[i]);
  });
  return out;
}

export async function api(method, path, body, { opId } = {}) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (opId) opts.headers['X-Operation-ID'] = opId;
  if (body !== undefined) opts.body = JSON.stringify(body);
  let res;
  try {
    res = await fetch(path, opts);
  } catch (e) {
    e.network = true;
    throw e;
  }
  if (res.status === 401) { window.location.href = '/login'; throw new Error('unauthorized'); }
  if (res.status === 204) return null;
  const data = await res.json();
  if (!res.ok) throw apiErrorFrom(res.status, data);
  return data;
}

function apiErrorFrom(status, data) {
  const err = new Error(data?.error || `HTTP ${status}`);
  err.tasks = data?.tasks || null;
  err.conflicts = data?.conflicts || null;
  err.status = status;
  err.duplicateOp = !!data?.duplicate_operation;
  return err;
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

// Relative age from a unix-seconds timestamp: "3d ago", "2h ago", "5m ago".
export function fmtAge(epoch) {
  if (!epoch) return '—';
  const secs = Math.max(0, Math.floor(Date.now() / 1000 - epoch));
  if (secs < 60) return 'just now';
  const m = Math.floor(secs / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 365) return `${d}d ago`;
  return `${Math.floor(d / 365)}y ago`;
}

export function fmtPct(p) {
  return p.toFixed(1) + '%';
}

export function fmtSpeed(mbps) {
  if (mbps < 0) return '—';
  if (mbps >= 1000) return (mbps / 1000) + ' Gbps';
  return mbps + ' Mbps';
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

function showOpLogRunning(title) {
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

// ── Operation sessions (#184) ─────────────────────────────────────────────────
// A session is one op-log dialog run. Every write inside it goes through
// session.api(), which tags the request with a fresh operation ID; live
// ansible.progress steps are shown only for the active session's IDs, in seq
// order. Gaps (dropped SSE events, reconnects) are filled from
// GET /api/v1/operations/{id}?after=<seq>, and a lost HTTP response is recovered
// by polling the same endpoint for the final result.

let activeSession = null;

export function newOpId() {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b); // randomUUID needs a secure context; plain-http LAN UIs don't have one
  return Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
}

// Called by the SSE client for every ansible.progress event.
export function opSessionStep(step) {
  activeSession?.onStep(step);
}

// Called by the SSE client on every (re)connect.
export function opSessionResync() {
  activeSession?.resync();
}

const sleep = ms => new Promise(r => setTimeout(r, ms));
const RECOVER_TIMEOUT_MS = 5 * 60_000;

export function startOp(title) {
  const session = {
    ids: new Set(),
    lastSeq: new Map(),
    running: true,
    syncing: new Set(),

    live() { return this.running && activeSession === this; },

    append(step) {
      const last = this.lastSeq.get(step.op) || 0;
      if (step.seq <= last) return true;      // duplicate
      if (step.seq !== last + 1) return false; // gap
      this.lastSeq.set(step.op, step.seq);
      appendOpLogStep(step);
      return true;
    },

    onStep(step) {
      if (!this.live() || !this.ids.has(step.op)) return;
      if (!this.append(step)) this.sync(step.op);
    },

    async sync(id) {
      if (this.syncing.has(id)) return;
      this.syncing.add(id);
      try {
        const op = await fetchOp(id, this.lastSeq.get(id) || 0);
        if (op && this.live()) for (const s of op.steps) this.append(s);
        return op;
      } finally {
        this.syncing.delete(id);
      }
    },

    resync() {
      if (!this.live()) return;
      for (const id of this.ids) this.sync(id).catch(() => {});
    },

    async api(method, path, body) {
      const id = newOpId();
      this.ids.add(id);
      try {
        return await api(method, path, body, { opId: id });
      } catch (e) {
        // network: the response was lost. duplicateOp: the browser
        // retransmitted the request after a connection reset and the server
        // refused to run it twice. Either way the original outcome is in
        // the operation record.
        if (!e.network && !e.duplicateOp) throw e;
        return await this.recover(id);
      }
    },

    // The HTTP response was lost: poll the operation record until it has
    // finished, then return/throw exactly as api() would have.
    async recover(id) {
      const deadline = Date.now() + RECOVER_TIMEOUT_MS;
      let delay = 1000;
      while (Date.now() < deadline) {
        await sleep(delay);
        delay = Math.min(delay * 2, 5000);
        let op;
        try {
          op = await fetchOp(id, this.lastSeq.get(id) || 0);
        } catch (e) {
          if (e.network) continue; // still offline
          throw e;
        }
        if (!op) throw new Error('Connection lost before the server recorded the operation — check its state and retry.');
        if (this.live()) for (const s of op.steps) this.append(s);
        if (op.state === 'running') continue;
        if (op.result_truncated) throw new Error(`Connection lost; the operation finished (${op.state}) but its result is too large to recover.`);
        if (op.http_status === 204) return null;
        if (op.http_status >= 400) throw apiErrorFrom(op.http_status, op.result);
        return op.result ?? null;
      }
      throw new Error('Connection lost; the operation did not report a result within 5 minutes.');
    },

    done(title, tasks, errorMsg, conflicts) {
      const wasActive = activeSession === this;
      this.running = false;
      if (wasActive) {
        activeSession = null;
        showOpLog(title, tasks, errorMsg, conflicts);
      } else {
        toast(errorMsg ? `${title}: ${errorMsg}` : title, errorMsg ? 'err' : 'ok');
      }
    },
  };
  activeSession = session;
  showOpLogRunning(title);
  return session;
}

// Returns the operation record (steps after `after`), or null if unknown/expired.
async function fetchOp(id, after) {
  try {
    return await api('GET', v1`/operations/${id}?after=${after}`);
  } catch (e) {
    if (e.status === 404) return null;
    throw e;
  }
}

export function showOpLog(title, tasks, errorMsg, conflicts) {
  document.getElementById('opLogTitle').textContent = title;
  const stepsEl = document.getElementById('opLogSteps');
  const errorEl = document.getElementById('opLogError');

  stepsEl.innerHTML = (tasks || []).map(t => `
    <div class="op-step ${esc(t.status)}">
      <span class="op-step-icon">${esc(stepIcons[t.status] || '?')}</span>
      <span class="op-step-name">${esc(t.name)}</span>
      ${t.msg ? `<span class="op-step-msg">${esc(t.msg)}</span>` : ''}
    </div>`).join('');

  if (conflicts?.length) {
    errorEl.innerHTML = `
      <div class="conflict-panel">
        <strong class="conflict-header">⚠ ${esc(errorMsg.split('\n')[0])}</strong>
        <div class="conflict-list">
          ${conflicts.map(c => `<div class="conflict-item"><span class="conflict-desc">${esc(c.describe)}</span></div>`).join('')}
        </div>
        <div class="conflict-hint">Remove these references first, then retry.</div>
      </div>`;
    errorEl.style.display = '';
  } else if (errorMsg) {
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
function appendOpLogStep(step) {
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

// ── Event delegation ──────────────────────────────────────────────────────────
// One listener per stable container, bound once at module init. Render
// functions replace the container's innerHTML freely; the listener survives.
// Dispatches on the closest [data-action] ancestor of the event target, so
// an inner button wins over an outer row that also carries data-action.
// Handlers receive (dataset, element, event).
export function delegate(container, handlers, type = 'click') {
  container.addEventListener(type, e => {
    const el = e.target.closest('[data-action]');
    if (!el || !container.contains(el)) return;
    const fn = handlers[el.dataset.action];
    if (fn) fn(el.dataset, el, e);
  });
}

// ── ZFS name validation regexes ───────────────────────────────────────────────
export const reZFSName   = /^[a-zA-Z][a-zA-Z0-9_.:-]*(\/[a-zA-Z][a-zA-Z0-9_.:-]*)*$/;
export const reSnapLabel = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$/;

// ── Refresh ───────────────────────────────────────────────────────────────────
export function setRefreshing(v) {
  document.getElementById('refreshBtn').classList.toggle('spinning', v);
}
