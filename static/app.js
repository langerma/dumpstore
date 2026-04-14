import { state, subscribe } from './js/store.js';
import { loadAll, startSSE, buildFormSelects } from './js/loader.js';
import { renderSysInfo, renderSoftware, renderNetwork, renderPools, renderIOStat, renderSMART } from './js/pools.js';
import { renderDatasets } from './js/datasets.js';
import { renderSnapshots } from './js/snapshots.js';
import { renderUsers, renderGroups, renderSambaUsers, renderSMBHomes, renderTimeMachine, renderSMBInitStatus } from './js/users.js';
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
subscribe(['smbInitialized', 'smbConfMtime'],                          renderSMBInitStatus);
subscribe(['sambaUsers', 'sambaAvailable', 'smbInitialized', 'users'], renderSambaUsers);
subscribe(['smbHomes', 'smbInitialized', 'datasets'],                  renderSMBHomes);
subscribe(['timeMachineShares', 'smbInitialized'],                     renderTimeMachine);
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

// ── TLS / HTTPS ───────────────────────────────────────────────────────────────
async function loadTLSStatus() {
  const wrap = document.getElementById('tls-status-wrap');
  try {
    const d = await api('GET', '/api/tls/status');
    if (!d.enabled || !d.cert_path) {
      wrap.innerHTML = `<p class="muted">HTTPS is not enabled. Configure a certificate below, then restart dumpstore with <code>--tls</code>.</p>`;
      return;
    }
    const expiry   = new Date(d.expires_at);
    const days     = d.days_remaining;
    const daysHtml = days < 30
      ? `<span style="color:var(--yellow)">${days} days</span>`
      : `${days} days`;
    const sans = (d.sans || []).map(s => esc(s)).join(', ') || '—';
    wrap.innerHTML = `
      <table class="data-table" style="max-width:540px">
        <tbody>
          <tr><td class="muted" style="width:140px">Status</td><td><span class="health-badge health-ONLINE">HTTPS active</span></td></tr>
          <tr><td class="muted">CN</td><td>${esc(d.cn || '—')}</td></tr>
          <tr><td class="muted">SANs</td><td>${sans}</td></tr>
          <tr><td class="muted">Expires</td><td>${esc(expiry.toLocaleDateString())} (${daysHtml})</td></tr>
          <tr><td class="muted">Type</td><td>${d.self_signed ? 'Self-signed' : d.acme_domain ? 'ACME / Let\'s Encrypt' : 'Custom'}</td></tr>
          ${d.acme_domain ? `<tr><td class="muted">ACME domain</td><td>${esc(d.acme_domain)}</td></tr>` : ''}
          <tr><td class="muted">Cert path</td><td><code>${esc(d.cert_path)}</code></td></tr>
        </tbody>
      </table>
      ${d.acme_domain ? `<button class="btn-secondary" style="margin-top:0.75rem" id="tlsAcmeRenewBtn">Renew (ACME)</button>` : ''}`;
    document.getElementById('tlsAcmeRenewBtn')?.addEventListener('click', tlsAcmeRenew);
  } catch { wrap.innerHTML = '<p class="muted">Could not load TLS status.</p>'; }
}

// Generate self-signed cert dialog
const tlsGencertDialog = document.getElementById('tlsGencertDialog');
document.getElementById('tlsGencertBtn').addEventListener('click', () => tlsGencertDialog.showModal());
document.getElementById('tlsGencertCancelBtn').addEventListener('click', () => tlsGencertDialog.close());
document.getElementById('tlsGencertForm').addEventListener('submit', async e => {
  e.preventDefault();
  const hostname = document.getElementById('tls-gencert-hostname').value.trim();
  const certDir  = document.getElementById('tls-gencert-certdir').value.trim();
  if (!hostname) { toast('Hostname is required.', 'err'); return; }
  tlsGencertDialog.close();
  showOpLogRunning('Generate TLS Certificate');
  try {
    const result = await api('POST', '/api/tls/gencert', { hostname, cert_dir: certDir });
    showOpLog('Generate TLS Certificate', result?.tasks);
    toast('Certificate generated. Restart dumpstore with --tls to activate HTTPS.', 'ok');
    loadTLSStatus();
  } catch (err) { showOpLog('Generate TLS Certificate', err.tasks, err.message); }
});

// Load existing cert dialog
const tlsPathDialog = document.getElementById('tlsPathDialog');
document.getElementById('tlsPathBtn').addEventListener('click', () => tlsPathDialog.showModal());
document.getElementById('tlsPathCancelBtn').addEventListener('click', () => tlsPathDialog.close());
document.getElementById('tlsPathForm').addEventListener('submit', async e => {
  e.preventDefault();
  const certPath = document.getElementById('tls-path-cert').value.trim();
  const keyPath  = document.getElementById('tls-path-key').value.trim();
  if (!certPath || !keyPath) { toast('Both cert and key paths are required.', 'err'); return; }
  tlsPathDialog.close();
  showOpLogRunning('Load TLS Certificate');
  try {
    const result = await api('PATCH', '/api/tls/config', { cert_path: certPath, key_path: keyPath });
    showOpLog('Load TLS Certificate', result?.tasks);
    toast('Certificate loaded. Restart dumpstore with --tls to activate HTTPS.', 'ok');
    loadTLSStatus();
  } catch (err) { showOpLog('Load TLS Certificate', err.tasks, err.message); }
});

// ACME / Let's Encrypt dialog
const tlsAcmeDialog = document.getElementById('tlsAcmeDialog');
document.getElementById('tlsAcmeBtn').addEventListener('click', () => tlsAcmeDialog.showModal());
document.getElementById('tlsAcmeCancelBtn').addEventListener('click', () => tlsAcmeDialog.close());
document.getElementById('tlsAcmeForm').addEventListener('submit', async e => {
  e.preventDefault();
  const email   = document.getElementById('tls-acme-email').value.trim();
  const domain  = document.getElementById('tls-acme-domain').value.trim();
  const certDir = document.getElementById('tls-acme-certdir').value.trim();
  if (!email || !domain) { toast('Email and domain are required.', 'err'); return; }
  tlsAcmeDialog.close();
  showOpLogRunning('Issue ACME Certificate');
  try {
    const result = await api('POST', '/api/tls/acme/issue', { email, domain, cert_dir: certDir });
    showOpLog('Issue ACME Certificate', result?.tasks);
    toast('Certificate issued. Restart dumpstore with --tls to activate HTTPS.', 'ok');
    loadTLSStatus();
  } catch (err) { showOpLog('Issue ACME Certificate', err.tasks, err.message); }
});

async function tlsAcmeRenew() {
  showOpLogRunning('Renew ACME Certificate');
  try {
    const result = await api('POST', '/api/tls/acme/renew');
    showOpLog('Renew ACME Certificate', result?.tasks);
    toast('Certificate renewed. Restart dumpstore to load the new cert.', 'ok');
    loadTLSStatus();
  } catch (err) { showOpLog('Renew ACME Certificate', err.tasks, err.message); }
}

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
loadTLSStatus();
loadAll();
startSSE();
