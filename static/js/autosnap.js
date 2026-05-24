import { state, storeSet } from './store.js';
import { api, esc, showOpLog, showOpLogRunning, toast } from './utils.js';

// renderAutoSnapStatus draws the banner above the Datasets table that reports
// who currently owns auto-snapshot execution. Three states:
//   1. dumpstore_managed=true  → green pill, no button.
//   2. os_daemon_active=true   → yellow pill + "Take over" button.
//   3. neither                 → grey "Auto-snapshot not running" + "Enable" button
//      (= register dumpstore's scheduler without running the takeover playbook).
export function renderAutoSnapStatus() {
  const el = document.getElementById('autosnap-status-banner');
  if (!el) return;
  const s = state.autosnapStatus;
  if (!s) { el.innerHTML = ''; return; }

  if (s.dumpstore_managed) {
    el.innerHTML = `
      <div class="autosnap-banner autosnap-banner-ok">
        <span class="health-badge badge-green">auto-snapshot</span>
        Managed by dumpstore — bucket snapshots run via the built-in scheduler.
        <button class="btn-secondary btn-small" id="autosnapReleaseBtn">Release to OS daemon</button>
      </div>`;
    document.getElementById('autosnapReleaseBtn').addEventListener('click', doRelease);
    return;
  }
  if (s.os_daemon_active) {
    el.innerHTML = `
      <div class="autosnap-banner autosnap-banner-warn">
        <span class="health-badge badge-yellow">auto-snapshot</span>
        Managed by <code>${esc(s.os_daemon)}</code>. Take over to fix property
        inheritance and remove the OS dependency.
        <button class="btn-primary btn-small" id="autosnapTakeoverBtn">Take over</button>
      </div>`;
    document.getElementById('autosnapTakeoverBtn').addEventListener('click', doTakeover);
    return;
  }
  el.innerHTML = `
    <div class="autosnap-banner autosnap-banner-warn">
      <span class="health-badge badge-yellow">auto-snapshot</span>
      No auto-snapshot daemon detected and dumpstore's scheduler is not registered. Bucket snapshots will not run.
      <button class="btn-primary btn-small" id="autosnapTakeoverBtn">Enable dumpstore scheduler</button>
    </div>`;
  document.getElementById('autosnapTakeoverBtn').addEventListener('click', doTakeover);
}

async function doTakeover() {
  showOpLogRunning('Auto-snapshot takeover');
  try {
    const result = await api('POST', '/api/auto-snapshot/takeover');
    showOpLog('Auto-snapshot takeover', result?.tasks);
    toast('dumpstore now manages auto-snapshots', 'ok');
    await refreshAutoSnapStatus();
  } catch (err) {
    showOpLog('Auto-snapshot takeover', err.tasks, err.message);
  }
}

async function doRelease() {
  if (!confirm('Stop running auto-snapshots from dumpstore and re-enable the OS daemon?')) return;
  showOpLogRunning('Auto-snapshot release');
  try {
    const result = await api('POST', '/api/auto-snapshot/release');
    showOpLog('Auto-snapshot release', result?.tasks);
    toast('Released back to OS daemon', 'ok');
    await refreshAutoSnapStatus();
  } catch (err) {
    showOpLog('Auto-snapshot release', err.tasks, err.message);
  }
}

export async function refreshAutoSnapStatus() {
  try {
    const s = await api('GET', '/api/auto-snapshot/status');
    storeSet('autosnapStatus', s);
  } catch (err) {
    console.warn('refreshAutoSnapStatus failed', err);
  }
}
