import { state, storeSet } from './store.js';
import { api, esc, startOp, toast, v1 } from './utils.js';

// renderAutoSnapStatus draws the banner above the Datasets table that reports
// who currently owns auto-snapshot execution. Three states:
//   1. dumpstore_managed=true  → green pill, no button.
//   2. os_daemon_active=true   → yellow pill + "Take over" button, or — when
//      zfstools runs from cron (cron_files) — the files to edit instead,
//      since dumpstore never edits a crontab and takeover returns 409.
//   3. neither                 → grey "Auto-snapshot not running" + "Enable" button
//      (= register dumpstore's scheduler without running the takeover playbook).
export function renderAutoSnapStatus() {
  const el = document.getElementById('autosnap-status-banner');
  if (!el) return;
  const s = state.autosnapStatus;
  if (!s) { el.innerHTML = ''; return; }

  const cronFiles = (s.cron_files || []).map(f => `<code>${esc(f)}</code>`).join(', ');
  if (s.dumpstore_managed) {
    const both = cronFiles
      ? ` <strong>zfstools also runs from cron</strong> (${cronFiles}) — remove its <code>zfs-auto-snapshot</code> lines so snapshots aren't taken twice.`
      : '';
    el.innerHTML = `
      <div class="autosnap-banner ${cronFiles ? 'autosnap-banner-warn' : 'autosnap-banner-ok'}">
        <span class="health-badge ${cronFiles ? 'badge-yellow' : 'badge-green'}">auto-snapshot</span>
        <span>Managed by dumpstore — bucket snapshots run via the built-in scheduler.${both}</span>
        <button class="btn-secondary btn-small" id="autosnapReleaseBtn">Release to OS daemon</button>
      </div>`;
    document.getElementById('autosnapReleaseBtn').addEventListener('click', doRelease);
    return;
  }
  if (s.os_daemon_active && cronFiles) {
    el.innerHTML = `
      <div class="autosnap-banner autosnap-banner-warn">
        <span class="health-badge badge-yellow">auto-snapshot</span>
        <span>Managed by <code>zfstools</code> from cron (${cronFiles}). To let dumpstore
        take over, remove the <code>zfs-auto-snapshot</code> lines there — dumpstore
        doesn't edit crontabs.</span>
      </div>`;
    return;
  }
  if (s.os_daemon_active) {
    el.innerHTML = `
      <div class="autosnap-banner autosnap-banner-warn">
        <span class="health-badge badge-yellow">auto-snapshot</span>
        <span>Managed by <code>${esc(s.os_daemon)}</code>. Take over to fix property
        inheritance and remove the OS dependency.</span>
        <button class="btn-primary btn-small" id="autosnapTakeoverBtn">Take over</button>
      </div>`;
    document.getElementById('autosnapTakeoverBtn').addEventListener('click', doTakeover);
    return;
  }
  el.innerHTML = `
    <div class="autosnap-banner autosnap-banner-warn">
      <span class="health-badge badge-yellow">auto-snapshot</span>
      <span>No auto-snapshot daemon detected and dumpstore's scheduler is not registered. Bucket snapshots will not run.</span>
      <button class="btn-primary btn-small" id="autosnapTakeoverBtn">Enable dumpstore scheduler</button>
    </div>`;
  document.getElementById('autosnapTakeoverBtn').addEventListener('click', doTakeover);
}

async function doTakeover() {
  const op = startOp('Auto-snapshot takeover');
  try {
    const result = await op.api('POST', v1`/auto-snapshot/takeover`);
    op.done('Auto-snapshot takeover', result?.tasks);
    toast('dumpstore now manages auto-snapshots', 'ok');
    await refreshAutoSnapStatus();
  } catch (err) {
    op.done('Auto-snapshot takeover', err.tasks, err.message);
  }
}

async function doRelease() {
  if (!confirm('Stop running auto-snapshots from dumpstore and re-enable the OS daemon?')) return;
  const op = startOp('Auto-snapshot release');
  try {
    const result = await op.api('POST', v1`/auto-snapshot/release`);
    op.done('Auto-snapshot release', result?.tasks);
    toast('Released back to OS daemon', 'ok');
    await refreshAutoSnapStatus();
  } catch (err) {
    op.done('Auto-snapshot release', err.tasks, err.message);
  }
}

export async function refreshAutoSnapStatus() {
  try {
    const s = await api('GET', v1`/auto-snapshot/status`);
    storeSet('autosnapStatus', s);
  } catch (err) {
    console.warn('refreshAutoSnapStatus failed', err);
  }
}
