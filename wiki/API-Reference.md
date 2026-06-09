# API Reference

All endpoints are served at `http://<host>:8080`. The API is JSON-over-HTTP; all request and response bodies are `application/json`.

## Endpoint overview

| Method | Path | Description |
|--------|------|-------------|
| GET    | `/api/sysinfo`              | Host and process info |
| GET    | `/api/network`              | Network interfaces (name, state, MAC, MTU, IPs, speed, RX/TX) |
| GET    | `/api/version`              | OpenZFS version string |
| GET    | `/api/pools`                | List all pools with usage stats |
| GET    | `/api/poolstatus`           | Detailed pool status with vdev tree |
| GET    | `/api/datasets`             | List all datasets and volumes |
| GET    | `/api/dataset-props/{name}` | Editable properties for a dataset |
| GET    | `/api/snapshots`            | List all snapshots |
| GET    | `/api/snapshots/diff`       | Files changed between snapshots (`zfs diff`) |
| GET    | `/api/iostat`               | Pool I/O statistics (1-second sample) |
| GET    | `/api/smart`                | S.M.A.R.T. health per disk |
| GET    | `/api/events`               | Server-Sent Events stream |
| GET    | `/metrics`                  | Prometheus text exposition |
| POST   | `/api/datasets`             | Create a dataset or volume |
| PATCH  | `/api/datasets/{name}`      | Update dataset properties |
| DELETE | `/api/datasets/{name}`      | Destroy a dataset or volume |
| POST   | `/api/rewrite/{name}`       | Rewrite existing blocks (`zfs rewrite`, background job) |
| POST   | `/api/snapshots`            | Create a snapshot |
| POST   | `/api/snapshots/clone`      | Clone a snapshot to a new dataset |
| POST   | `/api/snapshots/send`       | Send a snapshot to a target dataset (local or remote SSH) |
| DELETE | `/api/snapshots/{name}`     | Destroy a snapshot |
| GET    | `/api/users`                | List local users |
| POST   | `/api/users`                | Create a local user |
| PUT    | `/api/users/{name}`         | Edit user (shell, groups, password) |
| DELETE | `/api/users/{name}`         | Delete user and home directory |
| GET    | `/api/groups`               | List local groups |
| POST   | `/api/groups`               | Create a local group |
| PUT    | `/api/groups/{name}`        | Edit group (name, GID, members) |
| DELETE | `/api/groups/{name}`        | Delete a local group |
| GET    | `/api/chown/{dataset}`      | Get mountpoint owner and group |
| POST   | `/api/chown/{dataset}`      | Set mountpoint owner and/or group |
| GET    | `/api/acl-status`           | ACL presence map (dataset → bool) |
| GET    | `/api/acl/{dataset}`        | Get ACL entries for a dataset |
| POST   | `/api/acl/{dataset}`        | Add or modify an ACL entry |
| DELETE | `/api/acl/{dataset}`        | Remove an ACL entry |
| GET    | `/api/smb-shares`           | List active Samba usershares |
| POST   | `/api/smb-share/{dataset}`  | Create or update a Samba usershare |
| DELETE | `/api/smb-share/{dataset}`  | Remove a Samba usershare |
| GET    | `/api/smb-users`            | List users registered in smbpasswd |
| POST   | `/api/smb-users/{name}`     | Add a user to smbpasswd |
| DELETE | `/api/smb-users/{name}`     | Remove a user from smbpasswd |
| POST   | `/api/smb-config/pam`       | Run Samba setup playbook |
| GET    | `/api/smb/homes`            | Get current SMB [homes] config |
| POST   | `/api/smb/homes`            | Enable/update SMB [homes] section |
| DELETE | `/api/smb/homes`            | Disable/remove SMB [homes] section |
| GET    | `/api/smb/timemachine`      | List all Time Machine shares |
| POST   | `/api/smb/timemachine`      | Create/update a Time Machine share |
| DELETE | `/api/smb/timemachine/{name}` | Remove a Time Machine share |
| GET    | `/api/devices`              | List physical block devices (vdev candidates, in-use flag) |
| POST   | `/api/pools/{pool}/replace` | Replace a pool device (starts resilver) |
| POST   | `/api/pools/{pool}/offline` | Take a pool device offline |
| POST   | `/api/pools/{pool}/online`  | Bring a pool device online |
| POST   | `/api/scrub/{pool}`         | Start a pool scrub |
| DELETE | `/api/scrub/{pool}`         | Cancel a running pool scrub |
| GET    | `/api/scrub-schedules`      | List periodic scrub schedule config |
| PUT    | `/api/scrub-schedule/{pool}`| Add pool to periodic scrub schedule |
| DELETE | `/api/scrub-schedule/{pool}`| Remove pool from periodic scrub schedule |
| GET    | `/api/auto-snapshot/{dataset}` | Get auto-snapshot property values for a dataset |
| PUT    | `/api/auto-snapshot/{dataset}` | Set auto-snapshot properties for a dataset |
| GET    | `/api/auto-snapshot/status`    | Auto-snapshot ownership (OS daemon vs dumpstore) |
| POST   | `/api/auto-snapshot/takeover`  | Disable the OS daemon and run snapshots from dumpstore |
| POST   | `/api/auto-snapshot/release`   | Re-enable the OS daemon and stop dumpstore's scheduler |
| GET    | `/api/iscsi-targets`           | List all iSCSI targets |
| POST   | `/api/iscsi-targets`           | Create an iSCSI target for a zvol |
| DELETE | `/api/iscsi-targets`           | Remove an iSCSI target |
| GET    | `/api/services`                | List status of all managed services |
| POST   | `/api/services/{name}/{action}` | Control a service (start/stop/restart/enable/disable) |
| GET    | `/api/replication`             | List scheduled replication tasks |
| POST   | `/api/replication`             | Create a replication task |
| PATCH  | `/api/replication/{id}`        | Update a replication task |
| DELETE | `/api/replication/{id}`        | Delete a replication task |
| POST   | `/api/replication/{id}/run`    | Fire a replication task immediately (returns 202 + job_id) |
| GET    | `/api/replication/{id}/history` | Recent run records for a task |

---

## Datasets

### POST /api/datasets

Create a filesystem or volume.

```json
{
  "name": "tank/data",
  "type": "filesystem",
  "compression": "lz4",
  "quota": "50G",
  "mountpoint": "/mnt/data",
  "recordsize": "128K",
  "atime": "off",
  "exec": "on",
  "sync": "standard",
  "dedup": "off",
  "copies": "1",
  "xattr": "sa"
}
```

For volumes, use `"type": "volume"` and add `"volsize": "10G"`. Optional: `"volblocksize"`, `"sparse": true`.

### PATCH /api/datasets/{name}

Update dataset properties. Any subset of editable properties may be sent. An empty string value resets the property to inherited; a non-empty value sets it explicitly. Unknown properties are ignored.

```json
{
  "compression": "zstd",
  "quota": "",
  "readonly": "on"
}
```

Editable properties: `compression`, `quota`, `mountpoint`, `recordsize`, `atime`, `exec`, `sync`, `dedup`, `copies`, `xattr`, `readonly`, `acltype`, `sharenfs`, `sharesmb`.

### POST /api/rewrite/{name}

Rewrite the existing blocks of a mounted filesystem via `zfs rewrite` so that updated properties (compression, checksum, dedup, copies) apply to already-stored data. Runs as a **background job** — returns `202 Accepted` with a `job_id`; progress and output appear in the Jobs tab (`GET /api/jobs`).

```json
{
  "recursive": true,
  "skip_snapshot_shared": true,
  "skip_clone_shared": false
}
```

- `recursive` → `-r` (recurse into directories; crosses into child dataset mountpoints)
- `skip_snapshot_shared` → `-S` (don't duplicate blocks shared with snapshots)
- `skip_clone_shared` → `-C` (don't duplicate blocks shared via block cloning)

Caveats: `recordsize` changes are **not** applied by rewrite; rewriting blocks shared with snapshots/clones duplicates them (pool usage can increase) unless skipped; rewritten blocks appear as modified in incremental send streams. Volumes are rejected with `400` — rewrite operates through the mounted filesystem. Requires OpenZFS with `zfs rewrite` support (2.3+).

### DELETE /api/datasets/{name}

Destroy a dataset or volume. Append `?recursive=true` to also destroy all child datasets and snapshots.

Pool roots (e.g. `tank`) cannot be deleted via this endpoint — use `zpool destroy`.

---

## Snapshots

### POST /api/snapshots

```json
{
  "dataset": "tank/data",
  "snapname": "2024-01-15_backup",
  "recursive": false
}
```

### POST /api/snapshots/send

Dispatch `zfs send | zfs recv` as a background job. Returns `202 Accepted` immediately with the job metadata; live status is published on the `jobs.update` SSE topic and visible in the Jobs tab. Local target:

```json
{
  "snapshot": "tank/data@2026-05-06",
  "target": "backup/data",
  "incremental_from": "tank/data@2026-05-05",
  "raw": false
}
```

Add `"remote": "user@host"` to receive over SSH (`ssh -o BatchMode=yes`); the dumpstore service account must have SSH keys pre-configured for `host`. `incremental_from` and `raw` are optional. Response shape:

```json
{
  "job_id": "8f3c…",
  "type": "snapshot.send",
  "started_at": "2026-05-06T15:21:33Z",
  "snapshot": "tank/data@2026-05-06",
  "target": "user@host:backup/data"
}
```

Use `GET /api/jobs/{id}` to poll status, or subscribe to the `jobs.update` SSE topic.

### GET /api/snapshots/diff

Show files changed between two snapshots, or between a snapshot and the live dataset, via `zfs diff -H`.

Query parameters:
- `from` (required) — the older snapshot (`dataset@label`)
- `to` (optional) — a later snapshot of the **same dataset**; omit to diff against the current filesystem state

```json
{
  "from": "tank/data@before",
  "to": "tank/data@after",
  "truncated": false,
  "entries": [
    { "change": "+", "path": "/tank/data/new.txt" },
    { "change": "-", "path": "/tank/data/gone.txt" },
    { "change": "M", "path": "/tank/data/report.pdf" },
    { "change": "R", "path": "/tank/data/old.txt", "new_path": "/tank/data/renamed.txt" }
  ]
}
```

Entries are capped at 10 000 (`truncated: true` when more changed). The dataset must be mounted for `zfs diff` to work.

---

## Jobs

Long-running data-plane operations (currently snapshot send/receive) run as direct child processes outside Ansible, since they may run for hours. Each job has a status (`running`, `success`, `failed`, `cancelled`, `interrupted`), the captured argv, bounded stdout/stderr tails (last 64 KiB each), and timestamps. Records are persisted to disk so status survives a restart; any job left in `running` at shutdown is rewritten to `interrupted` on next boot.

### GET /api/jobs

Returns the list of known jobs, newest first.

### GET /api/jobs/{id}

Returns a single job's full record (including stdout/stderr tail).

### POST /api/jobs/{id}/cancel

Sends SIGTERM to the job's process group, escalating to SIGKILL after a 10 s grace. Returns `204 No Content` on success, `400` if the job is already terminal.

### DELETE /api/jobs/{id}

Removes a terminal job from the manager and deletes its on-disk record. Returns `204 No Content` on success, `400` if the job is still running (cancel it first).

---

## Scheduled replication

Cron-scheduled replication tasks. Each fire creates a `dumpstore-repl-<UTC>` snapshot on the source, holds it for the duration of the transfer, picks the most recent common `dumpstore-repl-*` snapshot for an incremental send, dispatches the pipeline through the jobs runner, releases the hold, and prunes destination replication snapshots beyond the retention count.

### GET /api/replication

Returns all replication tasks (without `last_runs`). Use `/history` to retrieve run records.

### POST /api/replication

Create a task. `enabled` and `retention_count` default to `true` and `7` respectively.

```json
{
  "name": "nightly-backup",
  "source": "tank/data",
  "target": "backup/data",
  "remote": "user@host",
  "schedule": "0 3 * * *",
  "retention_count": 7,
  "raw": false,
  "recursive": false,
  "enabled": true
}
```

`schedule` is a standard 5-field cron expression (minute, hour, day-of-month, month, day-of-week) with 1-minute resolution and no catch-up on missed firings. `remote` is optional; when empty the task replicates between local pools.

### PATCH /api/replication/{id}

Partial update; any subset of the above fields is accepted. Re-registers the task with the scheduler.

### DELETE /api/replication/{id}

Unregister and remove the task. Replicated snapshots on the destination, and `dumpstore-repl-*` snapshots on the source, are **not** deleted.

### POST /api/replication/{id}/run

Fire the task immediately, off-schedule. Returns `202 Accepted` with the job_id of the dispatched send/recv pipeline. The body of the running task continues asynchronously; subscribe to `jobs.update` for completion.

### GET /api/replication/{id}/history

Returns the recent `RunRecord` list (capped at 20):

```json
[
  {
    "job_id": "8f3c…",
    "snapshot": "tank/data@dumpstore-repl-20260524T030000Z",
    "started_at": "2026-05-24T03:00:00Z",
    "finished_at": "2026-05-24T03:11:42Z",
    "status": "success"
  }
]
```

### DELETE /api/snapshots/{dataset}@{snapname}

Append `?recursive=true` to also destroy clones.

---

## Devices & drive replacement

### GET /api/devices

Returns the physical block devices on the host (Linux: `/sys/block`, skipping loop/ram/zvol/dm pseudo-devices; FreeBSD: `geom disk list`). `in_use_by` carries the pool name when the device currently backs a vdev (best-effort matching: by-id/by-path symlinks resolved, partition suffixes stripped).

```json
[
  { "name": "sdb", "path": "/dev/sdb", "size_bytes": 4000787030016, "model": "WDC WD40EFRX", "in_use_by": "tank" },
  { "name": "sdc", "path": "/dev/sdc", "size_bytes": 4000787030016, "model": "WDC WD40EFRX", "in_use_by": "" }
]
```

### POST /api/pools/{pool}/replace

Replace a vdev with a new device via `zpool replace`. A resilver onto the new device starts automatically; progress is visible in the pool's `scan` field (`GET /api/poolstatus`, `poolstatus` SSE topic).

```json
{ "old_device": "sdb", "new_device": "/dev/sdc" }
```

`old_device` accepts a device name, path, or the numeric guid `zpool status` prints for missing devices. Returns Ansible task steps.

### POST /api/pools/{pool}/offline

Take a device offline for maintenance via `zpool offline`. Body: `{ "device": "sdb" }`. Returns Ansible task steps.

### POST /api/pools/{pool}/online

Bring an offlined device back via `zpool online`. Body: `{ "device": "sdb" }`. Returns Ansible task steps.

---

## Pool scrub

### POST /api/scrub/{pool}

Start a scrub on the named pool. Returns Ansible task steps.

### DELETE /api/scrub/{pool}

Cancel a running scrub on the named pool. Returns Ansible task steps.

### GET /api/scrub-schedules

Returns the current periodic scrub configuration for all pools.

```json
{
  "mode": "zfsutils",
  "schedules": [
    { "pool": "tank" }
  ]
}
```

`mode` is `"zfsutils"` on Linux (managed via `ZFS_SCRUB_POOLS` in `/etc/default/zfs`) or `"periodic"` on FreeBSD (managed via `daily_scrub_zfs_pools` in `/etc/periodic.conf`). On FreeBSD, `threshold_days` is also returned (default 35). An empty `schedules` array means all pools are scrubbed by the platform default.

### PUT /api/scrub-schedule/{pool}

Add a pool to the periodic scrub schedule. On FreeBSD, an optional `threshold_days` body field sets how many days must elapse before a scrub is triggered.

```json
{ "threshold_days": 35 }
```

Returns Ansible task steps.

### DELETE /api/scrub-schedule/{pool}

Remove a pool from the periodic scrub schedule. Returns Ansible task steps.

---

## Auto-snapshot scheduling

Manages `com.sun:auto-snapshot*` ZFS user properties per dataset. These properties are consumed by `zfs-auto-snapshot` (Linux) or `zfstools` (FreeBSD) to automatically create and rotate snapshots. dumpstore sets/clears the properties; the external daemon handles snapshot creation.

#### Default behaviour — important

`zfs-auto-snapshot` uses an **opt-out** model: any dataset where `com.sun:auto-snapshot` is **not explicitly set** is snapshotted by default. Setting the property to `false` is how you exclude a dataset.

The recommended pattern for snapshotting only specific datasets:

```bash
# 1. Opt the entire pool out
zfs set com.sun:auto-snapshot=false tank

# 2. Opt specific datasets back in
zfs set com.sun:auto-snapshot=true tank/data
zfs set com.sun:auto-snapshot=true tank/home
```

#### Inspect current config via CLI

```bash
# All datasets, all 6 properties
zfs get com.sun:auto-snapshot,com.sun:auto-snapshot:frequent,com.sun:auto-snapshot:hourly,com.sun:auto-snapshot:daily,com.sun:auto-snapshot:weekly,com.sun:auto-snapshot:monthly -t filesystem,volume

# Recursively from a pool root
zfs get -r com.sun:auto-snapshot tank

# Only locally-set values (excludes inherited/default)
zfs get -r -s local com.sun:auto-snapshot tank
```

### GET /api/auto-snapshot/{dataset}

Returns the current `com.sun:auto-snapshot*` property values and their source (local/inherited/default) for the given dataset.

```json
{
  "com.sun:auto-snapshot":          { "value": "true",  "source": "local" },
  "com.sun:auto-snapshot:frequent": { "value": "4",     "source": "local" },
  "com.sun:auto-snapshot:hourly":   { "value": "24",    "source": "local" },
  "com.sun:auto-snapshot:daily":    { "value": "7",     "source": "local" },
  "com.sun:auto-snapshot:weekly":   { "value": "4",     "source": "local" },
  "com.sun:auto-snapshot:monthly":  { "value": "-",     "source": "default" }
}
```

A `value` of `"-"` with `source` of `"default"` means the property is not set (inherits system default).

### PUT /api/auto-snapshot/{dataset}

Set or clear `com.sun:auto-snapshot*` properties on a dataset. Returns Ansible task steps.

**Request body** — any combination of these keys; omitted keys are left unchanged:

| Key | Values |
|-----|--------|
| `com.sun:auto-snapshot` | `"true"`, `"false"`, or `""` (inherit) |
| `com.sun:auto-snapshot:frequent` | integer 1–9999, or `""` (inherit) |
| `com.sun:auto-snapshot:hourly` | integer 1–9999, or `""` (inherit) |
| `com.sun:auto-snapshot:daily` | integer 1–9999, or `""` (inherit) |
| `com.sun:auto-snapshot:weekly` | integer 1–9999, or `""` (inherit) |
| `com.sun:auto-snapshot:monthly` | integer 1–9999, or `""` (inherit) |

Empty string (`""`) triggers `zfs inherit` on the property (clears the local value).

```json
{
  "com.sun:auto-snapshot": "true",
  "com.sun:auto-snapshot:daily": "7",
  "com.sun:auto-snapshot:monthly": "3"
}
```

---

## iSCSI targets

Expose ZFS volumes as iSCSI targets. Uses `targetcli`/LIO on Linux or `ctld` on FreeBSD. Endpoints return 501 if no backend is detected.

### GET /api/iscsi-targets

List all iSCSI targets backed by ZFS volumes.

```json
[
  {
    "iqn": "iqn.2024-03.io.dumpstore:tank-vms-win11",
    "zvol_name": "tank/vms/win11",
    "zvol_device": "/dev/zvol/tank/vms/win11",
    "lun": 0,
    "portals": ["0.0.0.0:3260"],
    "auth_mode": "none",
    "initiators": []
  }
]
```

### POST /api/iscsi-targets

Create an iSCSI target for a ZFS volume.

```json
{
  "zvol": "tank/vms/win11",
  "iqn": "iqn.2024-03.io.dumpstore:tank-vms-win11",
  "portal_ip": "0.0.0.0",
  "portal_port": "3260",
  "auth_mode": "none",
  "chap_user": "",
  "chap_password": "",
  "initiators": []
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `zvol` | yes | ZFS volume name, must contain `/` |
| `iqn` | yes | RFC 3720 iSCSI Qualified Name (`iqn.YYYY-MM.domain:name`) |
| `portal_ip` | no | Listen IP, defaults to `0.0.0.0` |
| `portal_port` | no | Listen port, defaults to `3260` |
| `auth_mode` | yes | `"none"` or `"chap"` |
| `chap_user` | when chap | CHAP username |
| `chap_password` | when chap | CHAP password |
| `initiators` | no | Array of allowed initiator IQNs; empty = allow all |

Returns Ansible task steps.

### DELETE /api/iscsi-targets?iqn=\<iqn\>&zvol=\<zvol\>

Remove an iSCSI target and its backstore. Both query parameters are required.

Returns Ansible task steps.

---

## Services

Manage the sharing daemons dumpstore controls (Samba, NFS, iSCSI). Status reads happen directly via `systemctl`/`service` — no Ansible overhead. Mutations (start/stop/restart/enable/disable) go through playbooks with op-log.

Logical service names: `samba`, `nfs`, `iscsi`.

### GET /api/services

Returns the status of all managed services.

```json
[
  {
    "name": "samba",
    "display_name": "Samba (SMB)",
    "unit_name": "smbd",
    "active": true,
    "enabled": true,
    "state": "active"
  }
]
```

`state` values: `active`, `inactive`, `failed`, `unknown`.

### POST /api/services/{name}/{action}

Control a service. Valid actions: `start`, `stop`, `restart`, `enable`, `disable`.

Returns Ansible task steps.

---

## ACLs

### GET /api/acl/{dataset}

Returns the ACL type and entries for the dataset's mountpoint.

```json
{
  "dataset": "tank/data",
  "mountpoint": "/mnt/data",
  "acl_type": "posix",
  "entries": [
    { "tag": "user",  "qualifier": "",      "perms": "rwx", "default": false },
    { "tag": "user",  "qualifier": "alice", "perms": "r-x", "default": false },
    { "tag": "group", "qualifier": "",      "perms": "r-x", "default": false },
    { "tag": "mask",  "qualifier": "",      "perms": "rwx", "default": false },
    { "tag": "other", "qualifier": "",      "perms": "---", "default": false }
  ]
}
```

`acl_type` is one of `"posix"`, `"nfsv4"`, or `"off"`.

For NFSv4 datasets each entry has the form:
```json
{ "tag": "A", "flags": "fd", "qualifier": "OWNER@", "perms": "rwaDxtTnNcCoy" }
```

### POST /api/acl/{dataset}

Add or modify an ACL entry. The `ace` string format depends on the dataset's `acltype`:

- **POSIX**: `setfacl -m` spec — `"user:alice:rwx"`, `"group:storage:r-x"`, `"default:user:alice:rwx"`
- **NFSv4**: full ACE string — `"A::alice@localdomain:rwaDxtTnNcCoy"`

```json
{ "ace": "user:alice:rwx", "recursive": false }
```

`recursive` (POSIX only) applies `setfacl -R` to all files inside the mountpoint.

### DELETE /api/acl/{dataset}?entry=\<spec\>

Remove an ACL entry. The `entry` query parameter:

- **POSIX**: `user:alice`, `default:group:storage`
- **NFSv4**: full ACE string to match

Append `&recursive=true` (POSIX only) to remove recursively.

---

## SMB Home Shares

Manage the Samba `[homes]` section in `smb.conf`. When enabled, each authenticated user automatically gets a personal share mapped to a subdirectory under the configured base path.

### GET /api/smb/homes

Returns the current `[homes]` configuration. If the section is not present, `enabled` is `false` and all fields are empty.

```json
{
  "enabled": true,
  "path": "/tank/homes/%S",
  "browseable": true,
  "read_only": false,
  "create_mask": "0644",
  "directory_mask": "0755"
}
```

### POST /api/smb/homes

Enable or update the `[homes]` section. Returns Ansible task steps.

```json
{
  "path": "/tank/homes/%S",
  "dataset": "tank/homes",
  "browseable": true,
  "read_only": false,
  "create_mask": "0644",
  "directory_mask": "0755"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `path` | yes | Base path for home directories (may include `%S` for username substitution) |
| `dataset` | no | ZFS dataset to use as base path (alternative to specifying `path` directly) |
| `browseable` | no | Whether the share is visible in browse lists (default `true`) |
| `read_only` | no | Whether the share is read-only (default `false`) |
| `create_mask` | no | File creation mask (default `"0644"`) |
| `directory_mask` | no | Directory creation mask (default `"0755"`) |

### DELETE /api/smb/homes

Remove the `[homes]` section from `smb.conf`. Returns Ansible task steps.

---

## Time Machine Shares

Manage Samba shares configured as macOS Time Machine backup targets using `vfs_fruit` with catia and streams_xattr VFS modules.

### GET /api/smb/timemachine

List all Time Machine shares. Parses `smb.conf` for sections with `fruit:time machine = yes`.

```json
[
  {
    "sharename": "timemachine",
    "path": "/tank/backups/timemachine",
    "max_size": "1T",
    "valid_users": "alice bob"
  }
]
```

### POST /api/smb/timemachine

Create or update a Time Machine share. Returns Ansible task steps.

```json
{
  "sharename": "timemachine",
  "dataset": "tank/backups",
  "path": "/tank/backups/timemachine",
  "max_size": "1T",
  "valid_users": "alice bob"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `sharename` | yes | Samba share name |
| `dataset` | no | ZFS dataset (alternative to specifying `path` directly) |
| `path` | yes | Filesystem path for the share |
| `max_size` | no | Maximum size quota for Time Machine backups |
| `valid_users` | no | Space-separated list of allowed users |

### DELETE /api/smb/timemachine/{sharename}

Remove a Time Machine share from `smb.conf`. Returns Ansible task steps.

---

## Server-Sent Events

### GET /api/events

Subscribe to live data updates. The server pushes named events whenever data changes.

**Query parameter:** `topics` — comma-separated list of topics to subscribe to.

**Available topics:**

| Topic            | Data                              | Cadence                     |
|------------------|-----------------------------------|-----------------------------|
| `pool.query`     | Same JSON as `GET /api/pools`     | Every 10 s on change        |
| `poolstatus`     | Same JSON as `GET /api/poolstatus`| Every 10 s on change        |
| `dataset.query`  | Same JSON as `GET /api/datasets`  | Every 10 s on change        |
| `snapshot.query` | Same JSON as `GET /api/snapshots` | Every 10 s on change        |
| `iostat`         | Same JSON as `GET /api/iostat`    | Every 10 s                  |
| `user.query`     | Same JSON as `GET /api/users`     | Every 10 s on change + after writes |
| `group.query`    | Same JSON as `GET /api/groups`    | Every 10 s on change + after writes |
| `ansible.progress` | Single `TaskStep` object        | Streamed during playbook run |

```
event: pool.query
data: [{"name":"tank","health":"ONLINE",...}]

event: iostat
data: [{"pool":"tank","read_ops":0,"write_ops":443,...}]
```

Example — watch pool health and I/O live:

```bash
curl -N 'http://localhost:8080/api/events?topics=pool.query,iostat'
```

---

## Error responses

Non-2xx responses return:

```json
{
  "error": "human-readable message",
  "tasks": [ ... ]
}
```

`tasks` is populated for Ansible-backed operations and contains the step results up to the point of failure.
