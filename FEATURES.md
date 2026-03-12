# dumpstore — Feature Tracker

## Implemented

| Feature | Since | Notes |
|---------|-------|-------|
| System info | v0.0.1 | Hostname, OS, kernel, CPU, uptime, load averages, process stats |
| Pool overview | v0.0.1 | Health badges, usage bars, fragmentation, dedup ratio, vdev tree |
| I/O statistics | v0.0.1 | Live read/write IOPS and bandwidth per pool |
| Disk health (SMART) | v0.0.1 | Temperature, power-on hours, reallocated/pending/uncorrectable sectors via `smartctl` |
| Dataset browser | v0.0.1 | Depth-indented collapsible tree; compression, quota, mountpoint |
| Dataset creation | v0.0.1 | Filesystems and volumes with all ZFS properties |
| Dataset editing | v0.0.1 | Update properties in place (set or inherit) |
| Dataset deletion | v0.0.1 | Recursive option; confirm-by-typing dialog |
| Snapshot management | v0.0.1 | List, create (recursive), delete |
| Prometheus metrics | v0.0.8 | HTTP request counters/latency, Ansible playbook counters/duration, Go runtime stats |
| Install script | v0.0.8 | `install.sh` — build, install, register service on Linux and FreeBSD |
| Live updates (SSE) | v0.0.2 | Server-Sent Events for pools, datasets, snapshots, I/O; falls back to REST polling |
| POSIX ACL management | v0.0.5 | View, add, remove entries via `getfacl`/`setfacl`; recursive apply |
| NFSv4 ACL management | v0.0.5 | View, add, remove entries via `nfs4_getfacl`/`nfs4_setfacl` |
| User management | v0.0.4 | List, create, edit, delete local Unix users; system accounts protected |
| Group management | v0.0.4 | List, create, edit, delete local Unix groups; system groups protected |
| Dataset mountpoint chown | v0.0.7 | View and set owner/group of a dataset's mountpoint |
| Software inventory | v0.0.7 | Versions of all runtime tools shown in Sysinfo tab |
| NFS share management | v0.0.9 | Enable/configure/disable NFS sharing via ZFS `sharenfs` property; cross-platform |
| SMB share management | v0.1.0 | Create/remove Samba usershares; manage Samba users; one-click Samba setup; cross-platform |
| Pool scrub management | v0.1.1 | Trigger scrubs, cancel running scrubs, view last scrub time/status/progress per pool |

---

## Planned

| Feature | Priority | Notes |
|---------|----------|-------|
| Auto-snapshot scheduling | High | Hourly/daily/weekly/monthly rotation policies; built-in scheduler (sanoid-style) |
| Pool scrub scheduling | Medium | Schedule periodic scrubs (cron-style) |
| ZFS native encryption | High | Load/unload keys, encryption status per dataset, keyformat/keylocation support |
| Dataset rename | Medium | Rename a dataset or volume in place |
| Snapshot clone | Medium | Create a new dataset from an existing snapshot |
| iSCSI target management | Medium | Expose zvols as iSCSI targets (`targetcli` on Linux, `ctld` on FreeBSD) |
| Pool import/export | Medium | Import available pools from attached devices; export pools safely |
| Snapshot diff | Medium | Show files changed between two snapshots (`zfs diff`) |
| Per-user quota tracking | Medium | Space usage per user/group (`zfs userspace` / `zfs groupspace`) |
| ZFS send/receive | Low | Pool replication and off-site backup |
| Alerts | Low | Configurable thresholds for pool health, disk temp, capacity |

---

## Cancelled / Out of Scope

| Feature | Reason |
|---------|--------|
| Snapshot rollback | Not aligned with project goals; too destructive for a lightweight UI |
| File browser | Scope creep; out of charter for a storage management tool |
