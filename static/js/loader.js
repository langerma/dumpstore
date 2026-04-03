import { state, storeSet, storeBatch } from './store.js';
import { api, esc, toast, setRefreshing, appendOpLogStep, opLogDialog } from './utils.js';
import { refreshACLStatus } from './datasets.js';

// ── Schema helpers ────────────────────────────────────────────────────────────
// Populate all schema-driven <select> elements from state.schema.
// Called once after loadAll() sets state.schema.
export function buildFormSelects() {
  const props = state.schema?.dataset_properties || [];
  for (const prop of props) {
    if (prop.input_type !== 'select') continue;
    const opts = prop.options.map(o => {
      const sel = o.default ? ' selected' : '';
      return `<option value="${esc(o.value)}"${sel}>${esc(o.label)}</option>`;
    }).join('');
    if (prop.create) {
      const el = document.getElementById('ds-' + prop.name);
      if (el) el.innerHTML = opts;
    }
    if (prop.editable) {
      const el = document.getElementById('edit-ds-' + prop.name);
      if (el) el.innerHTML = opts;
    }
  }

  const shells = state.schema?.user_shells || [];
  if (shells.length) {
    const opts = shells.map(s => `<option value="${esc(s)}">${esc(s)}</option>`).join('');
    const create = document.getElementById('user-shell');
    if (create) create.innerHTML = opts;
    const edit = document.getElementById('edit-user-shell');
    if (edit) edit.innerHTML = opts;
  }
}

// ── Load all ──────────────────────────────────────────────────────────────────
// Fast path: all endpoints that respond in <100 ms. Rendered immediately.
// Slow path: iostat (~1 s sampling) and SMART (drive scans) load separately
// so they never block the initial page render.
export async function loadAll() {
  setRefreshing(true);
  try {
    // Use null as the sentinel for failed fetches so we can distinguish
    // "endpoint returned empty" from "fetch failed" and preserve last-known-good state.
    const [pools, poolStatuses, version, sysinfo, datasets, snapshots, users, groups, smbData, smbShares, smbHomes, tmShares, iscsiTargets, scrubSchedules, autoSnapshotSchedules, schema] = await Promise.all([
      api('GET', '/api/pools').catch(() => null),
      api('GET', '/api/poolstatus').catch(() => null),
      api('GET', '/api/version').catch(() => null),
      api('GET', '/api/sysinfo').catch(() => null),
      api('GET', '/api/datasets').catch(() => null),
      api('GET', '/api/snapshots').catch(() => null),
      api('GET', '/api/users').catch(() => null),
      api('GET', '/api/groups').catch(() => null),
      api('GET', '/api/smb-users').catch(() => null),
      api('GET', '/api/smb-shares').catch(() => null),
      api('GET', '/api/smb/homes').catch(() => null),
      api('GET', '/api/smb/timemachine').catch(() => null),
      api('GET', '/api/iscsi-targets').catch(() => null),
      api('GET', '/api/scrub-schedules').catch(() => null),
      api('GET', '/api/auto-snapshot-schedules').catch(() => null),
      api('GET', '/api/schema').catch(() => null),
    ]);
    storeBatch(() => {
      if (pools !== null) storeSet('pools', pools);
      if (poolStatuses !== null) storeSet('poolStatuses', poolStatuses);
      if (version !== null) storeSet('version', version?.version || '');
      if (sysinfo !== null) storeSet('sysinfo', sysinfo);
      if (datasets !== null) storeSet('datasets', datasets);
      if (snapshots !== null) storeSet('snapshots', snapshots);
      if (users !== null) storeSet('users', users);
      if (groups !== null) storeSet('groups', groups);
      if (smbData !== null) {
        storeSet('sambaAvailable', smbData?.available ?? false);
        storeSet('sambaUsers', smbData?.users || []);
      }
      if (smbShares !== null) storeSet('smbShares', smbShares);
      if (smbHomes !== null) storeSet('smbHomes', smbHomes);
      if (tmShares !== null) storeSet('timeMachineShares', tmShares);
      if (iscsiTargets !== null) storeSet('iscsiTargets', iscsiTargets);
      if (scrubSchedules !== null) {
        const schedData = scrubSchedules || { mode: 'cron', schedules: [] };
        storeSet('scrubScheduleMode', schedData.mode || 'cron');
        storeSet('scrubThresholdDays', schedData.threshold_days || 35);
        storeSet('scrubSchedules', Object.fromEntries((schedData.schedules || []).map(s => [s.pool, s])));
      }
      if (autoSnapshotSchedules !== null) storeSet('autoSnapshot', autoSnapshotSchedules);
      if (schema !== null) storeSet('schema', schema);
    });
  } catch (e) {
    toast('Load failed: ' + e.message, 'err');
    console.error(e);
  } finally {
    setRefreshing(false);
  }
  refreshACLStatus();
  loadSlowMetrics();
}

// Loads iostat (~1 s) and SMART (drive scans) without blocking the main render.
export async function loadSlowMetrics() {
  const [iostat, smart] = await Promise.all([
    api('GET', '/api/iostat').catch(() => null),
    api('GET', '/api/smart').catch(() => null),
  ]);
  storeBatch(() => {
    if (iostat !== null) storeSet('iostat', iostat);
    if (smart !== null) storeSet('smart', smart);
  });
}

// ── SSE client ────────────────────────────────────────────────────────────────
// Maps SSE topic names → state key (render is handled by store subscriptions).
const sseTopicMap = {
  'pool.query':         'pools',
  'poolstatus':         'poolStatuses',
  'dataset.query':      'datasets',
  'autosnapshot.query': 'autoSnapshot',
  'snapshot.query':     'snapshots',
  'iostat':             'iostat',
  'user.query':         'users',
  'group.query':        'groups',
};

let _pollInterval = null;  // setInterval handle; null when SSE is active
let _sseRetryTimer = null; // setTimeout handle for SSE reconnect attempts
let _es = null;            // active EventSource instance

function setSseBadge(state) {
  const el = document.getElementById('sseStatus');
  if (!el) return;
  el.className = 'sse-badge ' + state;
  el.textContent = state;
}

function startPolling() {
  if (_pollInterval) return;
  _pollInterval = setInterval(loadAll, 30_000);
  setSseBadge('polling');
}

function stopPolling() {
  if (_pollInterval) { clearInterval(_pollInterval); _pollInterval = null; }
  setSseBadge('live');
}

export function startSSE() {
  if (_es) { _es.close(); _es = null; }
  const topics = [...Object.keys(sseTopicMap), 'ansible.progress'].join(',');
  const es = new EventSource('/api/events?topics=' + encodeURIComponent(topics));
  _es = es;

  es.onopen = () => {
    stopPolling();
    if (_sseRetryTimer) { clearTimeout(_sseRetryTimer); _sseRetryTimer = null; }
    // Always re-fetch all REST-only data (sysinfo, version, schema, smb config)
    // on every SSE (re)connect. SSE topics self-populate from the broker cache,
    // but endpoints not backed by SSE would otherwise stay stale/empty across
    // server restarts and post-reboot reconnects.
    loadAll();
  };

  es.addEventListener('ansible.progress', e => {
    try {
      const step = JSON.parse(e.data);
      // Only append if the dialog is open and still in running state (close button disabled)
      if (opLogDialog.open && document.getElementById('opLogClose').disabled) {
        appendOpLogStep(step);
      }
    } catch (err) { console.warn('[SSE] ansible.progress parse error', err); }
  });

  for (const [topic, key] of Object.entries(sseTopicMap)) {
    es.addEventListener(topic, e => {
      try {
        const v = JSON.parse(e.data);
        if (v !== null) storeSet(key, v);
      } catch (err) { console.warn('[SSE] parse error', topic, err); }
    });
  }

  es.onerror = () => {
    // Only fall back to polling when the browser has given up (CLOSED).
    // A transient CONNECTING state means the browser is already retrying.
    if (es.readyState === EventSource.CLOSED) {
      _es = null;
      // Immediately probe: if the session expired this will 401 → redirect to /login.
      loadAll();
      startPolling();
      if (!_sseRetryTimer) {
        _sseRetryTimer = setTimeout(() => { _sseRetryTimer = null; startSSE(); }, 5_000);
      }
    }
  };
}
