import { state, subscribe } from './js/store.js';
import { loadAll, startSSE, buildFormSelects } from './js/loader.js';
import { renderSysInfo, renderSoftware, renderPools, renderIOStat, renderSMART } from './js/pools.js';
import { renderDatasets } from './js/datasets.js';
import { renderSnapshots } from './js/snapshots.js';
import { renderUsers, renderGroups, renderSambaUsers, renderSMBHomes, renderTimeMachine } from './js/users.js';

// ── Store subscriptions ──────────────────────────────────────────────────────
subscribe(['sysinfo'],                                          renderSysInfo);
subscribe(['sysinfo'],                                          renderSoftware);
subscribe(['pools', 'poolStatuses', 'scrubSchedules',
           'scrubScheduleMode', 'scrubThresholdDays'],          renderPools);
subscribe(['iostat'],                                           renderIOStat);
subscribe(['smart'],                                            renderSMART);
subscribe(['datasets', 'aclStatus', 'smbShares',
           'iscsiTargets', 'autoSnapshot'],                     renderDatasets);
subscribe(['snapshots'],                                        renderSnapshots);
subscribe(['users'],                                            renderUsers);
subscribe(['groups'],                                           renderGroups);
subscribe(['sambaUsers', 'sambaAvailable', 'users'],            renderSambaUsers);
subscribe(['smbHomes', 'datasets'],                             renderSMBHomes);
subscribe(['timeMachineShares'],                                renderTimeMachine);
subscribe(['schema'],                                           buildFormSelects);

// ── Tabs ──────────────────────────────────────────────────────────────────────
document.querySelectorAll('.tab-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-pane').forEach(p => p.classList.remove('active'));
    btn.classList.add('active');
    state.activeTab = btn.dataset.tab;
    document.getElementById('tab-' + state.activeTab).classList.add('active');
  });
});

// ── Refresh ───────────────────────────────────────────────────────────────────
document.getElementById('refreshBtn').addEventListener('click', () => loadAll());

// ── Boot ──────────────────────────────────────────────────────────────────────
// Perform an immediate REST load so the UI is populated on first paint,
// then open the SSE stream. The SSE onopen handler cancels REST polling.
// If SSE is unavailable, startPolling() is called from the onerror handler.
//
// Safety-net: re-fetch all REST state every 60 s regardless of SSE health.
// SSE events can be silently dropped when the subscriber channel is full
// (broker logs "subscriber slow, dropping message"). When that happens the
// poller will not re-publish if the underlying data has not changed, leaving
// the browser permanently stale. This interval guarantees recovery.
setInterval(loadAll, 60_000);
loadAll();
startSSE();
