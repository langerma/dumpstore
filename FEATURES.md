# dumpstore — Feature Tracker

## Implemented

| Feature                  | Since  | Notes                                                                                                                          |
|--------------------------|--------|--------------------------------------------------------------------------------------------------------------------------------|
| System info              | v0.0.1 | Hostname, OS, kernel, CPU, uptime, load averages, process stats                                                                |
| Pool overview            | v0.0.1 | Health badges, usage bars, fragmentation, dedup ratio, vdev tree                                                               |
| I/O statistics           | v0.0.1 | Live read/write IOPS and bandwidth per pool                                                                                    |
| Disk health (SMART)      | v0.0.1 | Temperature, power-on hours, reallocated/pending/uncorrectable sectors via `smartctl`                                          |
| Dataset browser          | v0.0.1 | Depth-indented collapsible tree; compression, quota, mountpoint                                                                |
| Dataset creation         | v0.0.1 | Filesystems and volumes with all ZFS properties                                                                                |
| Dataset editing          | v0.0.1 | Update properties in place (set or inherit)                                                                                    |
| Dataset deletion         | v0.0.1 | Recursive option; confirm-by-typing dialog                                                                                     |
| Snapshot management      | v0.0.1 | List, create (recursive), delete single or multiple (batch delete with checkboxes)                                             |
| Prometheus metrics       | v0.0.8 | HTTP request counters/latency, Ansible playbook counters/duration, Go runtime stats                                            |
| Install script           | v0.0.8 | `install.sh` — build, install, register service on Linux and FreeBSD                                                           |
| Live updates (SSE)       | v0.0.2 | Server-Sent Events for pools, datasets, snapshots, I/O; falls back to REST polling                                             |
| POSIX ACL management     | v0.0.5 | View, add, remove entries via `getfacl`/`setfacl`; recursive apply                                                             |
| NFSv4 ACL management     | v0.0.5 | View, add, remove entries via `nfs4_getfacl`/`nfs4_setfacl`                                                                    |
| User management          | v0.0.4 | List, create, edit, delete local Unix users; system accounts protected                                                         |
| Group management         | v0.0.4 | List, create, edit, delete local Unix groups; system groups protected                                                          |
| Dataset mountpoint chown | v0.0.7 | View and set owner/group of a dataset's mountpoint                                                                             |
| Software inventory       | v0.0.7 | Versions of all runtime tools shown in Sysinfo tab                                                                             |
| NFS share management     | v0.0.9 | Enable/configure/disable NFS sharing via ZFS `sharenfs` property; cross-platform                                               |
| SMB share management     | v0.1.0 | Create/remove Samba usershares; manage Samba users; one-click Samba setup; cross-platform                                      |
| Pool scrub management    | v0.1.2 | Trigger scrubs, cancel running scrubs, view last scrub time/status/progress per pool                                           |
| Pool scrub scheduling    | v0.1.2 | Per-pool schedule via Linux `zfsutils-linux` (2nd Sunday monthly) or FreeBSD `periodic.conf` (configurable threshold)          |
| Auto-snapshot scheduling | v0.1.3 | Manage `com.sun:auto-snapshot*` ZFS properties per dataset; integrates with `zfs-auto-snapshot` (Linux) / `zfstools` (FreeBSD) |
| iSCSI target management  | v0.1.4 | Expose zvols as iSCSI targets (`targetcli`/LIO on Linux, `ctld` on FreeBSD); dialog with IQN, portal, CHAP auth, initiator ACLs |
| SMB home shares          | v0.1.5 | Enable/configure `[homes]` section in `smb.conf`; dataset picker or custom path; per-user auto-shares                           |
| Time Machine shares      | v0.1.6 | Samba `vfs_fruit` Time Machine backup targets; named shares backed by ZFS datasets; configurable max size and valid users         |
| User mgmt extensions     | v0.1.7 | SSH authorized key add/remove, home directory change with optional file migration, Samba password sync on edit                    |
| Request ID correlation   | v0.1.8 | Per-request `req_id` on all log lines; reads `X-Request-ID` from upstream proxies (nginx, Traefik) and echoes it back on response |

---

## Planned

| Feature                            | Priority | Issue | Notes                                                                                                                    |
|------------------------------------|----------|-------|--------------------------------------------------------------------------------------------------------------------------|
| Authentication                     | High     | [#51](https://github.com/langerma/dumpstore/issues/51) | Session-based login + reverse proxy `X-Remote-User` delegation |
| TLS / HTTPS                        | High     | [#52](https://github.com/langerma/dumpstore/issues/52) | Self-signed auto-gen, custom cert, optional ACME/Let's Encrypt |
| UPS / NUT integration              | High     | [#53](https://github.com/langerma/dumpstore/issues/53) | UPS status display; graceful shutdown on low battery via `upsc` |
| Drive replacement                  | High     | [#56](https://github.com/langerma/dumpstore/issues/56) | Replace faulted disks, monitor resilver progress, offline/online devices |
| Scheduled replication              | High     | [#54](https://github.com/langerma/dumpstore/issues/54) | Cron-based ZFS send/receive jobs with retention; depends on [#26](https://github.com/langerma/dumpstore/issues/26) |
| Pool create/import/export          | High     | [#23](https://github.com/langerma/dumpstore/issues/23) | Create pools (mirror, raidz1/2/3, draid); import/export existing pools |
| UI overhaul (datasets + snapshots) | Medium   | [#63](https://github.com/langerma/dumpstore/issues/63) | Purpose-driven redesign: dataset detail panel, pool/dataset hierarchy, snapshots grouped by dataset with filter/search |
| Dataset rename                     | Medium   | [#21](https://github.com/langerma/dumpstore/issues/21) | Rename a dataset or volume in place |
| Snapshot clone                     | Medium   | [#22](https://github.com/langerma/dumpstore/issues/22) | Create a new dataset from an existing snapshot |
| Snapshot scheduling UI             | Medium   | [#55](https://github.com/langerma/dumpstore/issues/55) | Manage auto-snapshot schedules and retention per dataset |
| Pool expansion                     | Medium   | [#57](https://github.com/langerma/dumpstore/issues/57) | Add vdevs, cache (L2ARC), log (SLOG), and spare devices to existing pools |
| Dataset rewrite                    | Medium   | [#50](https://github.com/langerma/dumpstore/issues/50) | Rewrite existing blocks to apply updated properties via `zfs rewrite`; exposed in Edit Dataset dialog |
| Snapshot diff                      | Medium   | [#24](https://github.com/langerma/dumpstore/issues/24) | Show files changed between two snapshots (`zfs diff`) |
| Per-user quota tracking            | Medium   | [#25](https://github.com/langerma/dumpstore/issues/25) | Space usage per user/group (`zfs userspace` / `zfs groupspace`) |
| Service management                 | Medium   | [#58](https://github.com/langerma/dumpstore/issues/58) | Start/stop/restart/enable Samba, NFS, iSCSI from the UI |
| Network interface overview         | Medium   | [#59](https://github.com/langerma/dumpstore/issues/59) | IP addresses, link state, speed — read-only, shown in Sysinfo tab |
| Log viewer                         | Medium   | [#60](https://github.com/langerma/dumpstore/issues/60) | Tail dumpstore logs, system journal, and ZFS events from the UI |
| lldap integration                  | Medium   | [#61](https://github.com/langerma/dumpstore/issues/61) | LDAP auth via lldap; Samba passthrough; user/group sync display |
| ZFS send/receive                   | Low      | [#26](https://github.com/langerma/dumpstore/issues/26) | One-shot pool replication; local and remote (SSH) |
| Alerts                             | Low      | [#27](https://github.com/langerma/dumpstore/issues/27) | Thresholds for pool health, disk temp, capacity; email/webhook delivery |
| Historical I/O graphs              | Low      | [#62](https://github.com/langerma/dumpstore/issues/62) | In-memory ring buffer; sparkline charts per pool; 5m/15m/1h range |
| ZFS native encryption              | Low      | [#20](https://github.com/langerma/dumpstore/issues/20) | Load/unload keys, keystatus display. **Deferred until [#51](https://github.com/langerma/dumpstore/issues/51) + [#52](https://github.com/langerma/dumpstore/issues/52) land** |
| OpenTelemetry                      | Low      | [#49](https://github.com/langerma/dumpstore/issues/49) | Traces, metrics, logs. **Deferred until collector infra available** |

---

## Cancelled / Out of Scope

| Feature           | Reason                                                               |
|-------------------|----------------------------------------------------------------------|
| Snapshot rollback | Not aligned with project goals; too destructive for a lightweight UI |
| File browser      | Scope creep; out of charter for a storage management tool            |
