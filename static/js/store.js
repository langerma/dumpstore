// ── State ────────────────────────────────────────────────────────────────────
export const state = {
  pools: [],
  poolStatuses: [],
  version: '',
  sysinfo: null,
  datasets: [],
  snapshots: [],
  iostat: [],
  smart: null,
  users: [],
  groups: [],
  sambaUsers: [],
  sambaAvailable: false,
  smbInitialized: false,  // true when smb.conf exists at the OS path
  smbConfPath: '',        // OS-specific path, from GET /api/v1/smb/status
  smbOs: '',              // 'linux' or 'freebsd', from GET /api/v1/smb/status
  smbConfMtime: '',       // RFC3339 mtime of smb.conf, from GET /api/v1/smb/status
  smbWsdd: null,          // wsdd (Windows discovery) status, from GET /api/v1/smb/status
  smbShares: [],      // [{name, path}] from net usershare info
  smbHomes: { enabled: false, path: '', browseable: 'no', read_only: 'no', create_mask: '0644', directory_mask: '0755' },
  timeMachineShares: [],  // [{name, path, max_size, valid_users}]
  iscsiTargets: [],   // [{iqn, zvol_name, ...}] from GET /api/v1/iscsi/targets
  activeTab: 'pools',
  collapsedDatasets: new Set(),
  selectedDataset: '',          // dataset shown in the detail drawer ('' = closed)
  aclDataset: '',
  aclData: null,
  aclStatus: {},
  hideSystemUsers: true,
  hideSystemGroups: true,
  scrubSchedules: {},      // pool name → ScrubSchedule
  scrubScheduleMode: 'cron',    // "cron" | "periodic"
  scrubThresholdDays: 35,       // FreeBSD periodic threshold (global)
  autoSnapshot: {},             // dataset name → AutoSnapshotProps
  selectedSnaps: new Set(),     // full snapshot names checked for batch delete
  collapsedSnapGroups: new Set(), // dataset names whose snapshot group is collapsed
  schema: null,                 // GET /api/v1/system/schema response
  network: null,                // GET /api/v1/system/network response
  services: [],                 // GET /api/v1/services — [{name, display_name, unit_name, active, enabled, state}]
  replication: [],              // GET /api/v1/replication/tasks — [Task]
  autosnapStatus: null,         // GET /api/v1/auto-snapshot/status — { os_daemon, os_daemon_active, dumpstore_managed, cron_files, last_runs }
};

// ── Reactive store ──────────────────────────────────────────────────────────
const _subs = {};          // key → Set<fn>
let _batching = false;
let _dirty = new Set();

export function subscribe(keys, fn) {
  for (const k of keys) {
    if (!_subs[k]) _subs[k] = new Set();
    _subs[k].add(fn);
  }
}

export function storeSet(key, value) {
  if (state[key] === value) return;
  state[key] = value;
  if (_batching) { _dirty.add(key); return; }
  _flush(new Set([key]));
}

export function storeBatch(fn) {
  _batching = true;
  _dirty = new Set();
  try { fn(); } finally {
    _batching = false;
    const keys = _dirty;
    _dirty = new Set();
    if (keys.size) _flush(keys);
  }
}

function _flush(keys) {
  const fns = new Set();
  for (const k of keys) {
    if (_subs[k]) for (const fn of _subs[k]) fns.add(fn);
  }
  for (const fn of fns) fn();
}
