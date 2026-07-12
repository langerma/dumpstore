import { state } from './store.js';
import { esc, api, delegate, toast, showOpLogRunning, showOpLog } from './utils.js';

export function renderServices() {
  const wrap = document.getElementById('services-wrap');
  if (!wrap) return;

  const svcs = state.services || [];
  if (!svcs.length) {
    wrap.innerHTML = '<p class="muted">No managed services found.</p>';
    return;
  }

  wrap.innerHTML = `
    <table class="data-table">
      <thead>
        <tr>
          <th>Service</th>
          <th>Unit</th>
          <th>Status</th>
          <th>Enabled</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${svcs.map(s => `
          <tr>
            <td>${esc(s.display_name)}</td>
            <td class="mono">${esc(s.unit_name)}</td>
            <td><span class="svc-badge ${esc(s.state)}">${esc(s.state)}</span></td>
            <td>${s.enabled
              ? '<span class="svc-enabled">yes</span>'
              : '<span class="svc-disabled">no</span>'
            }</td>
            <td class="svc-actions">
              ${s.active
                ? `<button class="btn-del" data-svc="${esc(s.name)}" data-action="stop">Stop</button>
                   <button class="btn-edit" data-svc="${esc(s.name)}" data-action="restart">Restart</button>`
                : `<button class="btn-edit" data-svc="${esc(s.name)}" data-action="start">Start</button>`
              }
              ${s.enabled
                ? `<button class="btn-del" data-svc="${esc(s.name)}" data-action="disable">Disable</button>`
                : `<button class="btn-edit" data-svc="${esc(s.name)}" data-action="enable">Enable</button>`
              }
            </td>
          </tr>`).join('')}
      </tbody>
    </table>`;
}

async function serviceAction(svc, action) {
  if (svc === 'nfs' && action === 'stop') {
    if (!confirm('Stopping NFS will disconnect all mounted clients. Continue?')) return;
  }

  showOpLogRunning(`${action} ${svc}`);
  try {
    const res = await api('POST', `/api/services/${svc}/${action}`);
    showOpLog(`${action} ${svc}`, res?.tasks);
    toast(`${svc} ${action} OK`, 'ok');
  } catch (err) {
    showOpLog(`${action} ${svc}`, err.tasks, err.message);
  }
}

// One delegated listener on the stable wrapper; survives renders.
const _svcHandler = action => ({ svc }) => serviceAction(svc, action);
delegate(document.getElementById('services-wrap'), {
  start:   _svcHandler('start'),
  stop:    _svcHandler('stop'),
  restart: _svcHandler('restart'),
  enable:  _svcHandler('enable'),
  disable: _svcHandler('disable'),
});
