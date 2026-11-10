import { state, storeSet, storeBatch } from './store.js';
import { api, esc, toast, setRefreshing, opSessionStep, opSessionResync, v1 } from './utils.js';
import { refreshACLStatus } from './dataset-dialogs.js';
import { mergeJob } from './jobs.js';

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
    // Editable props are rendered by the dataset drawer from the schema
    // directly — no static selects to populate.
  }

  // Gate draid topology options on the detected ZFS capability (#119):
  // draid needs OpenZFS >= 2.1, older hosts get disabled options.
  const caps = state.schema?.capabilities;
  if (caps) {
    document.querySelectorAll('#createPoolType option[value^="draid"]').forEach(o => {
      o.disabled = !caps.draid;
      o.title = caps.draid ? '' : 'requires OpenZFS ≥ 2.1 (not supported by the installed version)';
    });
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
    const [pools, poolStatuses, version, sysinfo, network, datasets, snapshots, users, groups, smbData, smbStatus, smbShares, smbHomes, tmShares, iscsiTargets, scrubSchedules, autoSnapshotSchedules, schema, services, jobs, replication, autosnapStatus] = await Promise.all([
      api('GET', v1`/pools`).catch(() => null),
      api('GET', v1`/pools/status`).catch(() => null),
      api('GET', v1`/system/version`).catch(() => null),
      api('GET', v1`/system/info`).catch(() => null),
      api('GET', v1`/system/network`).catch(() => null),
      api('GET', v1`/datasets`).catch(() => null),
      api('GET', v1`/snapshots`).catch(() => null),
      api('GET', v1`/users`).catch(() => null),
      api('GET', v1`/groups`).catch(() => null),
      api('GET', v1`/smb/users`).catch(() => null),
      api('GET', v1`/smb/status`).catch(() => null),
      api('GET', v1`/smb/shares`).catch(() => null),
      api('GET', v1`/smb/homes`).catch(() => null),
      api('GET', v1`/smb/timemachine`).catch(() => null),
      api('GET', v1`/iscsi/targets`).catch(() => null),
      api('GET', v1`/pools/scrub-schedules`).catch(() => null),
      api('GET', v1`/auto-snapshot/schedules`).catch(() => null),
      api('GET', v1`/system/schema`).catch(() => null),
      api('GET', v1`/services`).catch(() => null),
      api('GET', v1`/jobs`).catch(() => null),
      api('GET', v1`/replication/tasks`).catch(() => null),
      api('GET', v1`/auto-snapshot/status`).catch(() => null),
    ]);
    storeBatch(() => {
      if (pools !== null) storeSet('pools', pools);
      if (poolStatuses !== null) storeSet('poolStatuses', poolStatuses);
      if (version !== null) storeSet('version', version?.version || '');
      if (sysinfo !== null) storeSet('sysinfo', sysinfo);
      if (network !== null) storeSet('network', network);
      if (datasets !== null) storeSet('datasets', datasets);
      if (snapshots !== null) storeSet('snapshots', snapshots);
      if (users !== null) storeSet('users', users);
      if (groups !== null) storeSet('groups', groups);
      if (smbData !== null) {
        storeSet('sambaAvailable', smbData?.available ?? false);
        storeSet('sambaUsers', smbData?.users || []);
      }
      if (smbStatus !== null) {
        storeSet('smbInitialized', smbStatus?.initialized ?? false);
        storeSet('smbConfPath', smbStatus?.conf_path ?? '');
        storeSet('smbOs', smbStatus?.os ?? '');
        storeSet('smbConfMtime', smbStatus?.conf_mtime ?? '');
        storeSet('smbWsdd', smbStatus?.wsdd ?? null);
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
      if (services !== null) storeSet('services', services);
      if (jobs !== null) storeSet('jobs', jobs);
      if (replication !== null) storeSet('replication', replication);
      if (autosnapStatus !== null) storeSet('autosnapStatus', autosnapStatus);
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
    api('GET', v1`/pools/iostat`).catch(() => null),
    api('GET', v1`/system/smart`).catch(() => null),
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
  'service.query':      'services',
  'replication.update': 'replication',
  'autosnap.status':    'autosnapStatus',
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
  const topics = [...Object.keys(sseTopicMap), 'ansible.progress', 'jobs.update'].join(',');
  const es = new EventSource(v1`/events?topics=${topics}`);
  _es = es;

  es.onopen = () => {
    stopPolling();
    if (_sseRetryTimer) { clearTimeout(_sseRetryTimer); _sseRetryTimer = null; }
    // Always re-fetch all REST-only data (sysinfo, version, schema, smb config)
    // on every SSE (re)connect. SSE topics self-populate from the broker cache,
    // but endpoints not backed by SSE would otherwise stay stale/empty across
    // server restarts and post-reboot reconnects.
    loadAll();
    // Steps published while disconnected are fetched from the operation
    // record (#184).
    opSessionResync();
  };

  // Steps are tagged with their operation ID; the active op-log session
  // shows only its own, in seq order (#184).
  es.addEventListener('ansible.progress', e => {
    try {
      opSessionStep(JSON.parse(e.data));
    } catch (err) { console.warn('[SSE] ansible.progress parse error', err); }
  });

  // jobs.update publishes a single Job per event; merge into state.jobs by id.
  es.addEventListener('jobs.update', e => {
    try {
      const job = JSON.parse(e.data);
      if (job && job.id) mergeJob(job);
    } catch (err) { console.warn('[SSE] jobs.update parse error', err); }
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
