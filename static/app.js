import { state, subscribe } from './js/store.js';
import { loadAll, startSSE, buildFormSelects } from './js/loader.js';
import { renderSysInfo, renderSoftware, renderNetwork, renderPools, renderIOStat, renderSMART } from './js/pools.js';
import { renderDatasets } from './js/datasets.js';
import { renderSnapshots } from './js/snapshots.js';
import { renderUsers, renderGroups, renderSambaUsers, renderSMBHomes, renderTimeMachine } from './js/users.js';
import { renderServices } from './js/services.js';
import { api, esc, toast, showOpLog, showOpLogRunning } from './js/utils.js';

// ── Store subscriptions ──────────────────────────────────────────────────────
subscribe(['sysinfo'],                                          renderSysInfo);
subscribe(['sysinfo'],                                          renderSoftware);
subscribe(['network'],                                          renderNetwork);
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
subscribe(['services'],                                         renderServices);
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

// ── Auth header + config section ──────────────────────────────────────────────
async function initAuthHeader() {
  try {
    const data = await api('GET', '/api/whoami');
    const badge = document.getElementById('userBadge');
    const logoutBtn = document.getElementById('logoutBtn');
    if (data?.user) {
      badge.textContent = esc(data.user);
      badge.style.display = '';
      logoutBtn.style.display = '';
    }
  } catch { /* unauthenticated — middleware will redirect */ }
}

async function initAuthConfig() {
  const wrap = document.getElementById('auth-config-wrap');
  try {
    const data = await api('GET', '/api/auth/config');
    wrap.innerHTML = `
      <table class="data-table" style="max-width:420px">
        <tbody>
          <tr><td class="muted" style="width:140px">Username</td><td>${esc(data.username)}</td></tr>
          <tr><td class="muted">Session TTL</td><td>${esc(data.session_ttl)}</td></tr>
        </tbody>
      </table>`;
    document.getElementById('auth-new-username').value = data.username;
  } catch { wrap.innerHTML = ''; }
}

document.getElementById('logoutBtn').addEventListener('click', async () => {
  await fetch('/auth/logout', { method: 'POST' });
  window.location.href = '/login';
});

// Change Password dialog
const changePasswordDialog = document.getElementById('changePasswordDialog');
document.getElementById('changePasswordBtn').addEventListener('click', () => {
  document.getElementById('auth-current-pwd').value = '';
  document.getElementById('auth-new-pwd').value = '';
  document.getElementById('auth-confirm-pwd').value = '';
  changePasswordDialog.showModal();
});
document.getElementById('changePasswordCancelBtn').addEventListener('click', () => changePasswordDialog.close());
document.getElementById('changePasswordForm').addEventListener('submit', async e => {
  e.preventDefault();
  const cur = document.getElementById('auth-current-pwd').value;
  const np  = document.getElementById('auth-new-pwd').value;
  const cnf = document.getElementById('auth-confirm-pwd').value;
  if (np !== cnf) { toast('New passwords do not match.', 'err'); return; }
  if (!np)        { toast('New password must not be empty.', 'err'); return; }
  changePasswordDialog.close();
  showOpLogRunning('Change Password');
  try {
    const result = await api('POST', '/api/auth/change-password', { current_password: cur, new_password: np });
    showOpLog('Change Password', result?.tasks);
    toast('Password updated.', 'ok');
  } catch (err) { showOpLog('Change Password', err.tasks, err.message); }
});

// Change Username dialog
const changeUsernameDialog = document.getElementById('changeUsernameDialog');
document.getElementById('changeUsernameBtn').addEventListener('click', () => changeUsernameDialog.showModal());
document.getElementById('changeUsernameCancelBtn').addEventListener('click', () => changeUsernameDialog.close());
document.getElementById('changeUsernameForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username = document.getElementById('auth-new-username').value.trim();
  if (!username) { toast('Username must not be empty.', 'err'); return; }
  changeUsernameDialog.close();
  showOpLogRunning('Change Username');
  try {
    const result = await api('POST', '/api/auth/change-username', { username });
    showOpLog('Change Username', result?.tasks);
    // Server invalidated all sessions — redirect after user closes the op-log.
    document.getElementById('opLogClose').addEventListener('click', () => { window.location.href = '/login'; }, { once: true });
  } catch (err) { showOpLog('Change Username', err.tasks, err.message); }
});

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
initAuthHeader();
initAuthConfig();
loadAll();
startSSE();
