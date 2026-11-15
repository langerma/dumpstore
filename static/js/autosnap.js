import { state, storeSet } from './store.js';
import { api, delegate, esc, startOp, v1 } from './utils.js';

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
        <button class="btn-secondary btn-small" data-action="release">Release to OS daemon</button>
      </div>`;
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
        <button class="btn-primary btn-small" data-action="takeover">Take over</button>
      </div>`;
    return;
  }
  el.innerHTML = `
    <div class="autosnap-banner autosnap-banner-warn">
      <span class="health-badge badge-yellow">auto-snapshot</span>
      <span>No auto-snapshot daemon detected and dumpstore's scheduler is not registered. Bucket snapshots will not run.</span>
      <button class="btn-primary btn-small" data-action="takeover">Enable dumpstore scheduler</button>
    </div>`;
}

// The banner is re-rendered on every status change; its buttons dispatch
// through one listener on the stable container (#232).
delegate(document.getElementById('autosnap-status-banner'), {
  takeover: () => doTakeover(),
  release: () => doRelease(),
});

async function doTakeover() {
  const op = startOp('Auto-snapshot takeover');
  try {
    const result = await op.api('POST', v1`/auto-snapshot/takeover`);
    op.done('dumpstore now manages auto-snapshots', result?.tasks);
    await refreshAutoSnapStatus();
  } catch (err) {
    op.done('Auto-snapshot takeover failed', err.tasks, err.message);
  }
}

async function doRelease() {
  if (!confirm('Stop running auto-snapshots from dumpstore and re-enable the OS daemon?')) return;
  const op = startOp('Auto-snapshot release');
  try {
    const result = await op.api('POST', v1`/auto-snapshot/release`);
    op.done('Auto-snapshots released back to the OS daemon', result?.tasks);
    await refreshAutoSnapStatus();
  } catch (err) {
    op.done('Auto-snapshot release failed', err.tasks, err.message);
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
