import { state, storeSet, storeBatch } from './store.js';
import { api, esc, showOpLog, showOpLogRunning, toast } from './utils.js';

// ── Protected account denylists (must match handlers.go) ─────────────────────
const PROTECTED_USERS  = new Set(['nobody', 'nfsnobody']);
const PROTECTED_GROUPS = new Set(['nogroup', 'nobody', 'nfsnobody']);

// ── Render: Users ─────────────────────────────────────────────────────────────
export function renderUsers() {
  const btn = document.getElementById('toggleSystemUsersBtn');
  if (btn) btn.textContent = state.hideSystemUsers ? 'Show system' : 'Hide system';

  const wrap = document.getElementById('users-table-wrap');
  const users = state.hideSystemUsers
    ? state.users.filter(u => u.uid >= 1000 && !PROTECTED_USERS.has(u.username))
    : state.users;
  if (!users.length) {
    wrap.innerHTML = '<div class="loading">No users found.</div>';
    return;
  }
  const rows = users.map(u => {
    const isSystem = u.uid < 1000 || PROTECTED_USERS.has(u.username);
    return `
      <tr class="${isSystem ? 'row-muted' : ''}">
        <td class="mono">${esc(u.username)}</td>
        <td>${u.uid}</td>
        <td>${u.gid}</td>
        <td class="muted">${esc(u.shell)}</td>
        <td class="muted">${esc(u.home)}</td>
        <td>
          ${!isSystem ? `<button class="btn-edit" data-user="${esc(u.username)}">Edit</button>` : ''}
          ${!isSystem ? `<button class="btn-del" data-user="${esc(u.username)}">Delete</button>` : ''}
        </td>
      </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Username</th><th>UID</th><th>GID</th><th>Shell</th><th>Home</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.btn-edit[data-user]').forEach(btn => {
    btn.addEventListener('click', () => openEditUserDialog(btn.dataset.user));
  });
  wrap.querySelectorAll('.btn-del[data-user]').forEach(btn => {
    btn.addEventListener('click', () => openDeleteUserDialog(btn.dataset.user));
  });
}

// ── Render: Groups ────────────────────────────────────────────────────────────
export function renderGroups() {
  const btn = document.getElementById('toggleSystemGroupsBtn');
  if (btn) btn.textContent = state.hideSystemGroups ? 'Show system' : 'Hide system';

  const wrap = document.getElementById('groups-table-wrap');
  const groups = state.hideSystemGroups
    ? state.groups.filter(g => g.gid >= 1000 && !PROTECTED_GROUPS.has(g.name))
    : state.groups;
  if (!groups.length) {
    wrap.innerHTML = '<div class="loading">No groups found.</div>';
    return;
  }
  const rows = groups.map(g => {
    const isSystem = g.gid < 1000 || PROTECTED_GROUPS.has(g.name);
    return `
      <tr class="${isSystem ? 'row-muted' : ''}">
        <td class="mono">${esc(g.name)}</td>
        <td>${g.gid}</td>
        <td class="muted">${(g.members || []).map(esc).join(', ') || '—'}</td>
        <td>
          ${!isSystem ? `<button class="btn-edit" data-group="${esc(g.name)}">Edit</button>` : ''}
          ${!isSystem ? `<button class="btn-del" data-group="${esc(g.name)}">Delete</button>` : ''}
        </td>
      </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Group</th><th>GID</th><th>Members</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.btn-edit[data-group]').forEach(btn => {
    btn.addEventListener('click', () => openEditGroupDialog(btn.dataset.group));
  });
  wrap.querySelectorAll('.btn-del[data-group]').forEach(btn => {
    btn.addEventListener('click', () => openDeleteGroupDialog(btn.dataset.group));
  });
}

// ── System user/group toggles ─────────────────────────────────────────────────
document.getElementById('toggleSystemUsersBtn').addEventListener('click', () => {
  state.hideSystemUsers = !state.hideSystemUsers;
  renderUsers();
});
document.getElementById('toggleSystemGroupsBtn').addEventListener('click', () => {
  state.hideSystemGroups = !state.hideSystemGroups;
  renderGroups();
});

// ── New User dialog ───────────────────────────────────────────────────────────
const newUserDialog = document.getElementById('newUserDialog');
document.getElementById('newUserBtn').addEventListener('click', () => {
  document.getElementById('newUserForm').reset();
  document.getElementById('user-create-group').checked = true;
  newUserDialog.showModal();
});
document.getElementById('userCancelBtn').addEventListener('click', () => newUserDialog.close());

document.getElementById('newUserForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username    = document.getElementById('user-name').value.trim();
  const shell       = document.getElementById('user-shell').value;
  const uid         = document.getElementById('user-uid').value.trim();
  const group       = document.getElementById('user-group').value.trim();
  const groups      = document.getElementById('user-groups').value.trim();
  const password    = document.getElementById('user-password').value;
  const createGroup = document.getElementById('user-create-group').checked;
  const smb_user    = document.getElementById('user-smb').checked;
  newUserDialog.close();
  showOpLogRunning('Creating user…');
  try {
    const result = await api('POST', '/api/users', { username, shell, uid, group, groups, password, create_group: createGroup, smb_user });
    showOpLog(`User created: ${username}`, result.tasks, null);
    const [users, smbData] = await Promise.all([
      api('GET', '/api/users'),
      api('GET', '/api/smb-users').catch(() => null),
    ]);
    storeBatch(() => {
      storeSet('users', users || []);
      storeSet('sambaAvailable', smbData?.available ?? false);
      storeSet('sambaUsers', smbData?.users || []);
    });
  } catch (err) {
    showOpLog('User creation failed', err.tasks, err.message);
  }
});

// ── Delete User dialog ────────────────────────────────────────────────────────
const deleteUserDialog = document.getElementById('deleteUserDialog');
const deleteUserConfirmInput = document.getElementById('deleteUserConfirmInput');
const deleteUserConfirmBtn   = document.getElementById('deleteUserConfirmBtn');
let _deleteUserTarget = '';

function openDeleteUserDialog(username) {
  _deleteUserTarget = username;
  document.getElementById('deleteUserDisplayName').textContent = username;
  document.getElementById('deleteUserConfirmHint').textContent = username;
  deleteUserConfirmInput.value = '';
  deleteUserConfirmBtn.disabled = true;
  deleteUserDialog.showModal();
  deleteUserConfirmInput.focus();
}

deleteUserConfirmInput.addEventListener('input', () => {
  deleteUserConfirmBtn.disabled = deleteUserConfirmInput.value !== _deleteUserTarget;
});

document.getElementById('deleteUserCancelBtn').addEventListener('click', () => deleteUserDialog.close());

deleteUserConfirmBtn.addEventListener('click', async () => {
  if (deleteUserConfirmInput.value !== _deleteUserTarget) return;
  const username = _deleteUserTarget;
  deleteUserDialog.close();
  showOpLogRunning('Deleting user…');
  try {
    const result = await api('DELETE', '/api/users/' + encodeURIComponent(username));
    showOpLog(`Deleted user: ${username}`, result.tasks, null);
    const users = await api('GET', '/api/users');
    storeSet('users', users || []);
  } catch (e) {
    showOpLog(`Failed to delete user: ${username}`, e.tasks, e.message);
  }
});

// ── New Group dialog ──────────────────────────────────────────────────────────
const newGroupDialog = document.getElementById('newGroupDialog');
document.getElementById('newGroupBtn').addEventListener('click', () => {
  document.getElementById('newGroupForm').reset();
  newGroupDialog.showModal();
});
document.getElementById('groupCancelBtn').addEventListener('click', () => newGroupDialog.close());

document.getElementById('newGroupForm').addEventListener('submit', async e => {
  e.preventDefault();
  const groupname = document.getElementById('group-name').value.trim();
  const gid       = document.getElementById('group-gid').value.trim();
  newGroupDialog.close();
  showOpLogRunning('Creating group…');
  try {
    const result = await api('POST', '/api/groups', { groupname, gid });
    showOpLog(`Group created: ${groupname}`, result.tasks, null);
    const groups = await api('GET', '/api/groups');
    storeSet('groups', groups || []);
  } catch (e) {
    showOpLog('Group creation failed', e.tasks, e.message);
  }
});

// ── Delete Group dialog ───────────────────────────────────────────────────────
const deleteGroupDialog = document.getElementById('deleteGroupDialog');
const deleteGroupConfirmInput = document.getElementById('deleteGroupConfirmInput');
const deleteGroupConfirmBtn   = document.getElementById('deleteGroupConfirmBtn');
let _deleteGroupTarget = '';

function openDeleteGroupDialog(groupname) {
  _deleteGroupTarget = groupname;
  document.getElementById('deleteGroupDisplayName').textContent = groupname;
  document.getElementById('deleteGroupConfirmHint').textContent = groupname;
  deleteGroupConfirmInput.value = '';
  deleteGroupConfirmBtn.disabled = true;
  deleteGroupDialog.showModal();
  deleteGroupConfirmInput.focus();
}

deleteGroupConfirmInput.addEventListener('input', () => {
  deleteGroupConfirmBtn.disabled = deleteGroupConfirmInput.value !== _deleteGroupTarget;
});

document.getElementById('deleteGroupCancelBtn').addEventListener('click', () => deleteGroupDialog.close());

deleteGroupConfirmBtn.addEventListener('click', async () => {
  if (deleteGroupConfirmInput.value !== _deleteGroupTarget) return;
  const groupname = _deleteGroupTarget;
  deleteGroupDialog.close();
  showOpLogRunning('Deleting group…');
  try {
    const result = await api('DELETE', '/api/groups/' + encodeURIComponent(groupname));
    showOpLog(`Deleted group: ${groupname}`, result.tasks, null);
    const groups = await api('GET', '/api/groups');
    storeSet('groups', groups || []);
  } catch (e) {
    showOpLog(`Failed to delete group: ${groupname}`, e.tasks, e.message);
  }
});

// ── Edit User dialog ──────────────────────────────────────────────────────────
const editUserDialog = document.getElementById('editUserDialog');
let _editUserTarget = '';
let _editUserCurrentKeys = [];
let _editUserRemovedKeys = new Set();

function renderEditUserSSHKeys() {
  const list = document.getElementById('edit-user-sshkeys-list');
  if (!_editUserCurrentKeys.length) {
    list.innerHTML = '<span class="muted" style="font-size:12px">No authorized keys</span>';
    return;
  }
  list.innerHTML = _editUserCurrentKeys.map((key, i) => {
    const staged = _editUserRemovedKeys.has(key);
    return `<div class="sshkey-entry${staged ? ' staged-remove' : ''}" data-idx="${i}">
      <span class="sshkey-text" title="${esc(key)}">${esc(key)}</span>
      <button type="button" class="btn-small btn-secondary sshkey-toggle-remove" data-idx="${i}">${staged ? 'Undo' : '×'}</button>
    </div>`;
  }).join('');
  list.querySelectorAll('.sshkey-toggle-remove').forEach(btn => {
    btn.addEventListener('click', () => {
      const key = _editUserCurrentKeys[parseInt(btn.dataset.idx)];
      if (_editUserRemovedKeys.has(key)) _editUserRemovedKeys.delete(key);
      else _editUserRemovedKeys.add(key);
      renderEditUserSSHKeys();
    });
  });
}

async function openEditUserDialog(username) {
  const user = state.users.find(u => u.username === username);
  if (!user) return;
  _editUserTarget = username;
  _editUserRemovedKeys = new Set();
  _editUserCurrentKeys = [];
  document.getElementById('editUserDisplayName').textContent = username;

  const shellSel = document.getElementById('edit-user-shell');
  shellSel.value = user.shell;
  // If shell isn't in the list, add it as a temporary option
  if (shellSel.value !== user.shell) {
    const opt = document.createElement('option');
    opt.value = user.shell;
    opt.textContent = user.shell;
    shellSel.appendChild(opt);
    shellSel.value = user.shell;
  }

  document.getElementById('edit-user-home').value = user.home || '';
  document.getElementById('edit-user-move-home').checked = false;

  // Primary group: find group whose gid matches user's gid
  const primaryGroup = state.groups.find(g => g.gid === user.gid);
  document.getElementById('edit-user-group').value = primaryGroup ? primaryGroup.name : '';

  // Supplementary groups: groups where user is in members, excluding primary
  const suppGroups = state.groups
    .filter(g => g.gid !== user.gid && (g.members || []).includes(username))
    .map(g => g.name)
    .join(',');
  document.getElementById('edit-user-groups').value = suppGroups;

  document.getElementById('edit-user-password').value = '';
  document.getElementById('edit-user-smb-sync').checked = false;
  const isSambaUser = (state.sambaUsers || []).includes(username);
  document.getElementById('edit-user-smb-sync-row').style.display = isSambaUser ? '' : 'none';
  document.getElementById('edit-user-sshkey-new').value = '';

  // Show dialog immediately, load SSH keys async
  document.getElementById('edit-user-sshkeys-list').innerHTML =
    '<span class="muted" style="font-size:12px">Loading…</span>';
  editUserDialog.showModal();

  try {
    const data = await api('GET', '/api/users/' + encodeURIComponent(username) + '/sshkeys');
    _editUserCurrentKeys = data.keys || [];
  } catch (_) {
    _editUserCurrentKeys = [];
  }
  renderEditUserSSHKeys();
}

document.getElementById('editUserCancelBtn').addEventListener('click', () => editUserDialog.close());

document.getElementById('editUserForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username    = _editUserTarget;
  const shell       = document.getElementById('edit-user-shell').value;
  const group       = document.getElementById('edit-user-group').value.trim();
  const user_groups = document.getElementById('edit-user-groups').value.trim();
  const password    = document.getElementById('edit-user-password').value;
  const smb_sync    = document.getElementById('edit-user-smb-sync').checked;
  const home        = document.getElementById('edit-user-home').value.trim();
  const move_home   = document.getElementById('edit-user-move-home').checked;
  const newKey      = document.getElementById('edit-user-sshkey-new').value.trim();
  const removedKeys = [..._editUserRemovedKeys];
  editUserDialog.close();
  showOpLogRunning('Updating user…');

  const allTasks = [];
  try {
    const result = await api('PUT', '/api/users/' + encodeURIComponent(username),
      { shell, group, user_groups, password, home, move_home, smb_sync });
    allTasks.push(...(result.tasks || []));

    for (const key of removedKeys) {
      const r = await api('DELETE', '/api/users/' + encodeURIComponent(username) + '/sshkeys', { key });
      allTasks.push(...(r.tasks || []));
    }

    if (newKey) {
      const r = await api('POST', '/api/users/' + encodeURIComponent(username) + '/sshkeys', { key: newKey });
      allTasks.push(...(r.tasks || []));
    }

    showOpLog(`User updated: ${username}`, allTasks, null);
    const [users, groups] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/groups')]);
    storeBatch(() => {
      storeSet('users', users || []);
      storeSet('groups', groups || []);
    });
  } catch (err) {
    showOpLog(`Failed to update user: ${username}`, [...allTasks, ...(err.tasks || [])], err.message);
  }
});

// ── Edit Group dialog ─────────────────────────────────────────────────────────
const editGroupDialog = document.getElementById('editGroupDialog');
let _editGroupTarget = '';

function openEditGroupDialog(groupname) {
  const group = state.groups.find(g => g.name === groupname);
  if (!group) return;
  _editGroupTarget = groupname;
  document.getElementById('editGroupDisplayName').textContent = groupname;
  document.getElementById('edit-group-name').value = groupname;
  document.getElementById('edit-group-gid').value = '';
  document.getElementById('edit-group-members').value = (group.members || []).join(',');
  editGroupDialog.showModal();
}

document.getElementById('editGroupCancelBtn').addEventListener('click', () => editGroupDialog.close());

document.getElementById('editGroupForm').addEventListener('submit', async e => {
  e.preventDefault();
  const groupname = _editGroupTarget;
  const new_name  = document.getElementById('edit-group-name').value.trim();
  const gid       = document.getElementById('edit-group-gid').value.trim();
  const members   = document.getElementById('edit-group-members').value.trim();
  editGroupDialog.close();
  showOpLogRunning('Updating group…');
  try {
    const result = await api('PUT', '/api/groups/' + encodeURIComponent(groupname), { new_name, gid, members });
    showOpLog(`Group updated: ${result.groupname}`, result.tasks, null);
    const [users, groups] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/groups')]);
    storeBatch(() => {
      storeSet('users', users || []);
      storeSet('groups', groups || []);
    });
  } catch (err) {
    showOpLog(`Failed to update group: ${groupname}`, err.tasks, err.message);
  }
});

// ── Render: SMB Home Shares ───────────────────────────────────────────────────
export function renderSMBHomes() {
  const wrap = document.getElementById('smb-homes-wrap');
  if (!state.smbInitialized) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">Samba not initialised — click <strong>Initialize Samba</strong> above to get started.</div>';
    return;
  }
  const cfg = state.smbHomes;
  if (!cfg.enabled) {
    wrap.innerHTML = `
      <div style="display:flex;align-items:center;gap:1rem;padding:0.5rem 0">
        <span class="muted">Home shares are disabled.</span>
        <button class="btn-primary btn-small" id="smbHomesEnableBtn">Enable</button>
      </div>`;
    document.getElementById('smbHomesEnableBtn').addEventListener('click', openSMBHomesDialog);
  } else {
    wrap.innerHTML = `
      <div class="table-wrap">
        <table>
          <thead><tr><th>Path</th><th>Browseable</th><th>Read Only</th><th>Create Mask</th><th>Dir Mask</th><th></th></tr></thead>
          <tbody>
            <tr>
              <td class="mono">${esc(cfg.path)}</td>
              <td>${esc(cfg.browseable)}</td>
              <td>${esc(cfg.read_only)}</td>
              <td class="mono">${esc(cfg.create_mask)}</td>
              <td class="mono">${esc(cfg.directory_mask)}</td>
              <td><button class="btn-secondary btn-small smb-homes-edit-btn">Edit</button></td>
            </tr>
          </tbody>
        </table>
      </div>`;
    wrap.querySelector('.smb-homes-edit-btn').addEventListener('click', openSMBHomesDialog);
  }
}

// ── SMB Home Shares dialog ───────────────────────────────────────────────────
const smbHomesDialog = document.getElementById('smbHomesDialog');

function openSMBHomesDialog() {
  const cfg = state.smbHomes;

  // Populate dataset picker
  const sel = document.getElementById('smb-homes-dataset');
  sel.innerHTML = '<option value="">— custom path —</option>';
  const fsList = (state.datasets || []).filter(d => d.type === 'filesystem' && d.mountpoint && d.mountpoint !== '-' && d.mountpoint !== 'none');
  fsList.forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.mountpoint;
    opt.textContent = d.name + ' (' + d.mountpoint + ')';
    sel.appendChild(opt);
  });

  // Pre-fill from current config
  const pathInput = document.getElementById('smb-homes-path');
  if (cfg.enabled && cfg.path) {
    pathInput.value = cfg.path;
    // Try to match dataset
    const base = cfg.path.replace(/\/%U$/, '');
    const matchOpt = Array.from(sel.options).find(o => o.value === base);
    if (matchOpt) sel.value = base;
  } else {
    pathInput.value = '';
    sel.value = '';
  }

  document.getElementById('smb-homes-browseable').value = cfg.browseable || 'no';
  document.getElementById('smb-homes-readonly').value = cfg.read_only || 'no';
  document.getElementById('smb-homes-create-mask').value = cfg.create_mask || '0644';
  document.getElementById('smb-homes-directory-mask').value = cfg.directory_mask || '0755';

  // Show/hide disable button
  const disableBtn = document.getElementById('smbHomesDisableBtn');
  const applyBtn = document.getElementById('smbHomesApplyBtn');
  disableBtn.style.display = cfg.enabled ? '' : 'none';
  applyBtn.textContent = cfg.enabled ? 'Apply' : 'Enable';

  smbHomesDialog.showModal();
}

// Dataset picker → auto-fill path
document.getElementById('smb-homes-dataset').addEventListener('change', function() {
  const pathInput = document.getElementById('smb-homes-path');
  if (this.value) {
    pathInput.value = this.value + '/%U';
  } else {
    pathInput.value = '';
  }
});

document.getElementById('smbHomesCancelBtn').addEventListener('click', () => smbHomesDialog.close());

document.getElementById('smbHomesApplyBtn').addEventListener('click', async () => {
  const path = document.getElementById('smb-homes-path').value.trim();
  if (!path) { toast('Path is required', 'err'); return; }
  const body = {
    path,
    browseable: document.getElementById('smb-homes-browseable').value,
    read_only: document.getElementById('smb-homes-readonly').value,
    create_mask: document.getElementById('smb-homes-create-mask').value.trim(),
    directory_mask: document.getElementById('smb-homes-directory-mask').value.trim(),
  };
  smbHomesDialog.close();
  showOpLogRunning('Configuring home shares…');
  try {
    const result = await api('POST', '/api/smb/homes', body);
    showOpLog('Home shares enabled', result.tasks, null);
    storeSet('smbHomes', result.config);
  } catch (err) {
    showOpLog('Failed to enable home shares', err.tasks, err.message);
  }
});

document.getElementById('smbHomesDisableBtn').addEventListener('click', async () => {
  smbHomesDialog.close();
  showOpLogRunning('Disabling home shares…');
  try {
    const result = await api('DELETE', '/api/smb/homes');
    showOpLog('Home shares disabled', result.tasks, null);
    storeSet('smbHomes', { enabled: false, path: '', browseable: 'no', read_only: 'no', create_mask: '0644', directory_mask: '0755' });
  } catch (err) {
    showOpLog('Failed to disable home shares', err.tasks, err.message);
  }
});

// ── Render: Time Machine Shares ───────────────────────────────────────────────
export function renderTimeMachine() {
  const wrap = document.getElementById('tm-shares-wrap');
  if (!state.smbInitialized) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">Samba not initialised — click <strong>Initialize Samba</strong> above to get started.</div>';
    return;
  }
  const shares = state.timeMachineShares || [];
  if (!shares.length) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">No Time Machine shares configured.</div>';
    return;
  }
  const rows = shares.map(s => `
    <tr>
      <td class="mono">${esc(s.name)}</td>
      <td class="mono">${esc(s.path)}</td>
      <td>${s.max_size ? esc(s.max_size) : '<span class="muted">no limit</span>'}</td>
      <td>${s.valid_users ? esc(s.valid_users) : '<span class="muted">all</span>'}</td>
      <td><button class="btn-del btn-small tm-del-btn" data-name="${esc(s.name)}">Delete</button></td>
    </tr>`).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Share</th><th>Path</th><th>Max Size</th><th>Valid Users</th><th></th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.tm-del-btn').forEach(btn => {
    btn.addEventListener('click', () => deleteTimeMachineShare(btn.dataset.name));
  });
}

async function deleteTimeMachineShare(name) {
  if (!confirm(`Delete Time Machine share "${name}"?`)) return;
  showOpLogRunning('Removing Time Machine share…');
  try {
    const result = await api('DELETE', '/api/smb/timemachine/' + encodeURIComponent(name));
    showOpLog(`Time Machine share deleted: ${name}`, result.tasks, null);
    const shares = await api('GET', '/api/smb/timemachine').catch(() => []);
    storeSet('timeMachineShares', shares || []);
  } catch (err) {
    showOpLog(`Failed to delete Time Machine share: ${name}`, err.tasks, err.message);
  }
}

// ── Time Machine dialog ──────────────────────────────────────────────────────
const timeMachineDialog = document.getElementById('timeMachineDialog');

function openTimeMachineDialog() {
  document.getElementById('tm-sharename').value = '';
  document.getElementById('tm-path').value = '';
  document.getElementById('tm-max-size').value = '';
  document.getElementById('tm-valid-users').value = '';

  // Populate dataset picker
  const sel = document.getElementById('tm-dataset');
  sel.innerHTML = '<option value="">— custom path —</option>';
  const fsList = (state.datasets || []).filter(d => d.type === 'filesystem' && d.mountpoint && d.mountpoint !== '-' && d.mountpoint !== 'none');
  fsList.forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.mountpoint;
    opt.textContent = d.name + ' (' + d.mountpoint + ')';
    sel.appendChild(opt);
  });
  sel.value = '';

  timeMachineDialog.showModal();
  document.getElementById('tm-sharename').focus();
}

document.getElementById('tmAddBtn').addEventListener('click', openTimeMachineDialog);

document.getElementById('tm-dataset').addEventListener('change', function() {
  const pathInput = document.getElementById('tm-path');
  if (this.value) {
    pathInput.value = this.value;
  } else {
    pathInput.value = '';
  }
});

document.getElementById('tmCancelBtn').addEventListener('click', () => timeMachineDialog.close());

document.getElementById('tmCreateBtn').addEventListener('click', async () => {
  const sharename = document.getElementById('tm-sharename').value.trim();
  const path = document.getElementById('tm-path').value.trim();
  if (!sharename) { toast('Share name is required', 'err'); return; }
  if (!path) { toast('Path is required', 'err'); return; }
  const body = {
    sharename,
    path,
    max_size: document.getElementById('tm-max-size').value.trim(),
    valid_users: document.getElementById('tm-valid-users').value.trim(),
  };
  timeMachineDialog.close();
  showOpLogRunning('Creating Time Machine share…');
  try {
    const result = await api('POST', '/api/smb/timemachine', body);
    showOpLog('Time Machine share created', result.tasks, null);
    storeSet('timeMachineShares', result.shares || []);
  } catch (err) {
    showOpLog('Failed to create Time Machine share', err.tasks, err.message);
  }
});

// ── Render: SMB Users ─────────────────────────────────────────────────────────
export function renderSambaUsers() {
  const wrap = document.getElementById('smb-users-wrap');
  if (!state.smbInitialized) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">Samba not initialised — click <strong>Initialize Samba</strong> above to get started.</div>';
    return;
  }
  if (!state.sambaAvailable) {
    wrap.innerHTML = '<div class="muted" style="padding:0.5rem 0">Samba (pdbedit) not available on this system.</div>';
    return;
  }
  const smbSet = new Set(state.sambaUsers);
  const regularUsers = state.users.filter(u => u.uid >= 1000 && !PROTECTED_USERS.has(u.username));
  if (!regularUsers.length) {
    wrap.innerHTML = '<div class="loading">No regular users found.</div>';
    return;
  }
  const rows = regularUsers.map(u => {
    const registered = smbSet.has(u.username);
    const statusHtml = registered
      ? '<span class="smb-status smb-yes">● registered</span>'
      : '<span class="smb-status smb-no">○ not registered</span>';
    const actionBtn = registered
      ? `<button class="btn-del smb-remove-btn" data-user="${esc(u.username)}">Remove from SMB</button>`
      : `<button class="btn-secondary smb-add-btn" data-user="${esc(u.username)}">Add to SMB</button>`;
    return `
      <tr>
        <td class="mono">${esc(u.username)}</td>
        <td>${u.uid}</td>
        <td>${statusHtml}</td>
        <td>${actionBtn}</td>
      </tr>`;
  }).join('');
  wrap.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr>
          <th>Username</th><th>UID</th><th>SMB Status</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  wrap.querySelectorAll('.smb-add-btn').forEach(btn => {
    btn.addEventListener('click', () => openAddSmbUserDialog(btn.dataset.user));
  });
  wrap.querySelectorAll('.smb-remove-btn').forEach(btn => {
    btn.addEventListener('click', () => removeSmbUser(btn.dataset.user));
  });
}

// ── Add SMB User dialog ───────────────────────────────────────────────────────
const addSmbUserDialog = document.getElementById('addSmbUserDialog');
let _addSmbUserTarget = '';

function openAddSmbUserDialog(username) {
  _addSmbUserTarget = username;
  document.getElementById('addSmbUserDisplayName').textContent = username;
  document.getElementById('smb-user-password').value = '';
  addSmbUserDialog.showModal();
  document.getElementById('smb-user-password').focus();
}

document.getElementById('addSmbUserCancelBtn').addEventListener('click', () => addSmbUserDialog.close());

document.getElementById('addSmbUserForm').addEventListener('submit', async e => {
  e.preventDefault();
  const username = _addSmbUserTarget;
  const password = document.getElementById('smb-user-password').value;
  addSmbUserDialog.close();
  showOpLogRunning('Adding SMB user…');
  try {
    const result = await api('POST', '/api/smb-users/' + encodeURIComponent(username), { password });
    showOpLog(`SMB access added: ${username}`, result.tasks, null);
    const smbData = await api('GET', '/api/smb-users').catch(() => null);
    storeBatch(() => {
      storeSet('sambaAvailable', smbData?.available ?? false);
      storeSet('sambaUsers', smbData?.users || []);
    });
  } catch (err) {
    showOpLog(`Failed to add SMB access: ${username}`, err.tasks, err.message);
  }
});

// ── Remove SMB User dialog ────────────────────────────────────────────────────
const removeSmbUserDialog = document.getElementById('removeSmbUserDialog');
let _removeSmbUserTarget = '';

function removeSmbUser(username) {
  _removeSmbUserTarget = username;
  document.getElementById('removeSmbUserDisplayName').textContent = username;
  removeSmbUserDialog.showModal();
}

document.getElementById('removeSmbUserCancelBtn').addEventListener('click', () => removeSmbUserDialog.close());

document.getElementById('removeSmbUserConfirmBtn').addEventListener('click', async () => {
  const username = _removeSmbUserTarget;
  removeSmbUserDialog.close();
  showOpLogRunning('Removing SMB user…');
  try {
    const result = await api('DELETE', '/api/smb-users/' + encodeURIComponent(username));
    showOpLog(`SMB access removed: ${username}`, result.tasks, null);
    const smbData = await api('GET', '/api/smb-users').catch(() => null);
    storeBatch(() => {
      storeSet('sambaAvailable', smbData?.available ?? false);
      storeSet('sambaUsers', smbData?.users || []);
    });
  } catch (err) {
    showOpLog(`Failed to remove SMB access: ${username}`, err.tasks, err.message);
  }
});

// ── Configure Samba dialog ────────────────────────────────────────────────────
const configureSambaDialog = document.getElementById('configureSambaDialog');
document.getElementById('smbConfigPamBtn').addEventListener('click', () => configureSambaDialog.showModal());
document.getElementById('configureSambaCancelBtn').addEventListener('click', () => configureSambaDialog.close());

document.getElementById('configureSambaConfirmBtn').addEventListener('click', async () => {
  configureSambaDialog.close();
  showOpLogRunning('Initialising Samba…');
  try {
    const result = await api('POST', '/api/smb/init');
    showOpLog('Samba initialised', result.tasks, null);
    const status = await api('GET', '/api/smb/status').catch(() => null);
    if (status !== null) {
      storeBatch(() => {
        storeSet('smbInitialized', status?.initialized ?? false);
        storeSet('smbConfPath', status?.conf_path ?? '');
        storeSet('smbOs', status?.os ?? '');
      });
    }
  } catch (err) {
    showOpLog('Failed to initialise Samba', err.tasks, err.message);
  }
});
