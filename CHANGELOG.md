# Changelog

All notable changes to this project will be documented here.

## [v0.0.5] — 2026-03-04

### Added
- **POSIX ACL management** — view, add, and remove POSIX ACL entries on mounted datasets via `GET/POST/DELETE /api/acl/{dataset}`; uses `getfacl` / `setfacl`
- **NFSv4 ACL management** — same API, uses `nfs4_getfacl` / `nfs4_setfacl`; `acl` and `nfs4-acl-tools` are optional runtime dependencies
- `acltype` property editable via `PATCH /api/datasets/{name}` (`off`, `posix`, `nfsv4`)
- ACL tab in dataset row: shows current entries, add-entry form, enable/disable controls
- "Disable ACLs" button in ACL dialog (uses `<dialog>` pattern, not `confirm()`)
- Mandatory POSIX base entries (`user::`, `group::`, `other::`) shown without a delete button to prevent invalid ACL state

### Fixed
- **systemd mount namespace** — removed `PrivateTmp=true` and `ProtectSystem=strict/full` from the service unit; both options create an isolated mount namespace with slave propagation, causing `zfs create` to not auto-mount and `zfs destroy` to see datasets as busy
- `zfs destroy` now uses `-f` (force unmount) to reliably remove mounted datasets when invoked from the service
- Ansible task failure messages now include `stderr`/`stdout` in addition to the generic `msg` field, making ZFS errors visible in logs and the op-log dialog

### Changed
- All Ansible task names capitalised to satisfy `ansible-lint` name-casing rule

## [v0.0.4] — 2026-03-04

### Added
- **Local user management** — list, create, edit, and delete local Unix users via `GET/POST /api/users` and `DELETE /api/users/{name}`
- **Local group management** — list, create, edit, and delete local Unix groups via `GET/POST /api/groups` and `DELETE /api/groups/{name}`
- Users & Groups tab in the UI with system-account rows shown muted (no delete button)
- Type-to-confirm delete dialogs for users and groups
- Ansible playbooks: `user_create.yml`, `user_delete.yml`, `group_create.yml`, `group_delete.yml`
- `internal/system/system.go` — parses `/etc/passwd`, `/etc/group`, `/etc/login.defs` for user/group reads
- SSE topics `user.query` and `group.query` pushed on write ops and every 10 s on change

### Changed
- `/etc` write operations (useradd/userdel/groupadd/groupdel) protected by a mutex to avoid concurrent modification
- System accounts (UID/GID < `UID_MIN`) are protected from deletion at both API and playbook level (403)
- `nobody` / `nogroup` explicitly guarded against deletion regardless of UID/GID
- README updated with Users & Groups API, SSE topics table, and planned features table

## [v0.0.3] — 2026-03-04

### Changed
- Replace placeholder text logo with SVG lockup in the UI header
- Add SVG favicon (`dumpstore-blue-dark-icon48.svg`)
- Update README to use dark/light-mode-aware SVG logos via `<picture>`
- Add `images/` directory with full set of logo variants (blue/mono, dark/light, icon48/icon80/lockup)

## [v0.0.2] — 2026-02-xx

### Added
- **Live updates via SSE** — Server-Sent Events endpoint (`GET /api/events`) pushes pool, dataset, snapshot, and I/O changes every 10 s; browser falls back to 30 s REST polling if the connection is lost
- Subscription broker (`internal/broker`) with per-topic pub/sub and change detection (JSON equality check)
- Background ZFS poller goroutine that publishes only on data change
- Dark/light mode logo variants in README
- Screenshots in README

## [v0.0.1] — 2026-01-xx

### Added
- Initial release
- Go HTTP server (stdlib only, no external dependencies)
- **System info** — hostname, OS, kernel, CPU, uptime, load averages, process stats
- **Pool overview** — health badges, usage bars, fragmentation, deduplication ratio, vdev tree
- **I/O statistics** — live read/write IOPS and bandwidth per pool
- **Disk health** — S.M.A.R.T. data per drive via `smartctl`
- **Dataset browser** — collapsible tree, compression, quota, mountpoint
- **Dataset management** — create, edit properties, and delete (with confirm-by-typing dialog) filesystems and volumes
- **Snapshot management** — list, create (recursive), and delete snapshots
- Ansible playbook runner for write operations with structured JSON output
- Prometheus metrics endpoint (`GET /metrics`)
- systemd unit file (Linux) and rc.d script (FreeBSD)
- `make install` with OS-aware service registration
