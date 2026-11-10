# API Reference

All endpoints are served at `http://<host>:8080`. The API is JSON-over-HTTP; all request and response bodies are `application/json`.

**Machine-readable spec:** an OpenAPI 3.1 document generated from the server's route registry is served at `GET /api/v1/openapi.json` (authenticated) and committed in the repository as [`api/openapi.json`](https://github.com/langerma/dumpstore/blob/main/api/openapi.json). Load it into any OpenAPI tool (Swagger UI, Redocly, client generators). Dataset, snapshot and device names contain `/` and `@`; generated clients percent-encode them in path parameters, which the server accepts. Some request and response bodies are still documented as generic JSON objects; they get full schemas in Phase 2 of [#181](https://github.com/langerma/dumpstore/issues/181).

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
| GET    | `/api/v1/openapi.json`      | OpenAPI 3.1 description of this API |
| GET    | `/api/operations/{id}`      | Write operation record: state, steps (`?after=<seq>`), final status + result |
| GET    | `/metrics`                  | Prometheus text exposition |
| GET    | `/api/schema`               | Dataset property definitions and system metadata for the current OS |
| POST   | `/api/datasets`             | Create a dataset or volume |
| PATCH  | `/api/datasets/{name}`      | Update dataset properties |
| DELETE | `/api/datasets/{name}`      | Destroy a dataset or volume |
| POST   | `/api/datasets/rename`      | Rename a dataset or volume (`{"name","new_name"}`, same parent) |
| POST   | `/api/rewrite/{name}`       | Rewrite existing blocks (`zfs rewrite`, background job) |
| GET    | `/api/userspace/{name}`     | Per-user/group space usage (`?kind=user\|group`) |
| POST   | `/api/userquota/{name}`     | Set or remove a per-user/group quota |
| POST   | `/api/snapshots`            | Create a snapshot |
| POST   | `/api/snapshots/clone`      | Clone a snapshot to a new dataset |
| POST   | `/api/snapshots/send`       | Send a snapshot to a target dataset (local or remote SSH) |
| POST   | `/api/snapshots/delete-batch` | Destroy several snapshots (`{"snapshots":[...]}`) |
| DELETE | `/api/snapshots/{name}`     | Destroy a snapshot |
| GET    | `/api/users`                | List local users |
| POST   | `/api/users`                | Create a local user |
| PUT    | `/api/users/{name}`         | Edit user (shell, groups, password) |
| DELETE | `/api/users/{name}`         | Delete user and home directory |
| GET    | `/api/users/{name}/sshkeys` | List a user's SSH authorized keys |
| POST   | `/api/users/{name}/sshkeys` | Add an SSH authorized key |
| DELETE | `/api/users/{name}/sshkeys` | Remove an SSH authorized key (`{"key":"ssh-ed25519 …"}`) |
| GET    | `/api/groups`               | List local groups |
| POST   | `/api/groups`               | Create a local group |
| PUT    | `/api/groups/{name}`        | Edit group (name, GID, members) |
| DELETE | `/api/groups/{name}`        | Delete a local group |
| GET    | `/api/chown/{dataset}`      | Get mountpoint owner and group |
| POST   | `/api/chown/{dataset}`      | Set mountpoint owner and group (both required) |
| GET    | `/api/acl-status`           | Per-dataset ACL flag (dataset → bool; `true` when `acltype` is not `off`) |
| GET    | `/api/acl/{dataset}`        | Get ACL entries for a dataset |
| POST   | `/api/acl/{dataset}`        | Add or modify an ACL entry |
| DELETE | `/api/acl/{dataset}`        | Remove an ACL entry |
| GET    | `/api/smb-shares`           | List Samba usershares, including stale ones whose path is gone (`path_missing`) |
| POST   | `/api/smb-share/{dataset}`  | Create or update a Samba usershare |
| DELETE | `/api/smb-share/{dataset}`  | Remove a Samba usershare |
| GET    | `/api/smb-users`            | List users in the Samba user database (`pdbedit`) |
| POST   | `/api/smb-users/{name}`     | Add a user to the Samba user database |
| DELETE | `/api/smb-users/{name}`     | Remove a user from the Samba user database |
| GET    | `/api/smb/status`           | Samba init state, conf path, OS, wsdd status |
| POST   | `/api/smb/init`             | Initialise Samba (back up existing config, render smb.conf) |
| POST   | `/api/smb/wsdd`             | Render wsdd options from smb.conf (take over wsdd) |
| GET    | `/api/smb/homes`            | Get current SMB [homes] config |
| POST   | `/api/smb/homes`            | Enable/update SMB [homes] section |
| DELETE | `/api/smb/homes`            | Disable/remove SMB [homes] section |
| GET    | `/api/smb/timemachine`      | List all Time Machine shares |
| POST   | `/api/smb/timemachine`      | Create/update a Time Machine share |
| DELETE | `/api/smb/timemachine/{name}` | Remove a Time Machine share |
| GET    | `/api/devices`              | List physical block devices (vdev candidates, in-use flag) |
| POST   | `/api/pools`                | Create a pool (`zpool create`) |
| GET    | `/api/pools/importable`     | List pools available for import |
| POST   | `/api/pools/import`         | Import a pool (`zpool import [-f]`) |
| POST   | `/api/pools/{pool}/export`  | Export a pool (`zpool export`) |
| POST   | `/api/pools/{pool}/vdevs`   | Add a data vdev (`zpool add`) |
| POST   | `/api/pools/{pool}/cache`   | Add an L2ARC cache device |
| POST   | `/api/pools/{pool}/log`     | Add a SLOG log device (optionally mirrored) |
| POST   | `/api/pools/{pool}/spare`   | Add a hot spare |
| DELETE | `/api/pools/{pool}/devices/{device}` | Remove a cache/log/spare device (`zpool remove`) |
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
| GET    | `/api/auto-snapshot-schedules` | Auto-snapshot properties for all datasets |
| GET    | `/api/iscsi-targets`           | List all iSCSI targets |
| POST   | `/api/iscsi-targets`           | Create an iSCSI target for a zvol |
| DELETE | `/api/iscsi-targets`           | Remove an iSCSI target |
| GET    | `/api/iscsi/status`            | iSCSI backend and ctld init state |
| POST   | `/api/iscsi/init`              | Take ownership of `/etc/ctl.conf` (FreeBSD) |
| GET    | `/api/services`                | List status of all managed services |
| POST   | `/api/services/{name}/{action}` | Control a service (start/stop/restart/enable/disable) |
| GET    | `/api/replication`             | List scheduled replication tasks |
| POST   | `/api/replication`             | Create a replication task |
| PATCH  | `/api/replication/{id}`        | Update a replication task |
| DELETE | `/api/replication/{id}`        | Delete a replication task |
| POST   | `/api/replication/{id}/run`    | Fire a replication task immediately (returns 202 + job_id) |
| GET    | `/api/replication/{id}/history` | Recent run records for a task |
| GET    | `/api/jobs`                    | List background jobs |
| GET    | `/api/jobs/{id}`               | Single job status |
| POST   | `/api/jobs/{id}/cancel`        | Cancel a running job (SIGTERM, then SIGKILL) |
| DELETE | `/api/jobs/{id}`               | Remove a finished job record |
| GET    | `/api/tls/status`              | TLS configuration and certificate metadata |
| POST   | `/api/tls/gencert`             | Generate a self-signed certificate |
| PATCH  | `/api/tls/config`              | Use an existing certificate + key from disk |
| POST   | `/api/tls/acme/issue`          | Issue a certificate via ACME (`lego`) |
| POST   | `/api/tls/acme/renew`          | Renew the ACME certificate |
| GET    | `/login`                       | Login page |
| POST   | `/auth/login`                  | Log in (form: `username`, `password`); sets the session cookie |
| POST   | `/auth/logout`                 | Log out |
| GET    | `/api/whoami`                  | Current identity (session or trusted-proxy `X-Remote-User`) |
| GET    | `/api/auth/config`             | Configured username and session TTL |
| POST   | `/api/auth/change-password`    | Change the login password |
| POST   | `/api/auth/change-username`    | Change the login username |

---

## Authentication

All routes except `/login`, `/auth/*`, `/images/*` and the paths in `unprotected_paths` (default `["/metrics"]`, prefix match) require one of:

- a valid `dumpstore_session` cookie (from `POST /auth/login`), or
- an `X-Remote-User` header on a request whose source IP is inside a `trusted_proxies` CIDR.

Unauthenticated requests to `/api/*` (or with `Accept: application/json`) get `401 {"error":"unauthorized"}`; anything else is redirected (`302`) to `/login`.

If no password is configured, dumpstore binds to `127.0.0.1:8080` only, and no login can succeed until one is set with `dumpstore --set-password`.

Sessions are kept in memory: all sessions are lost on restart. Expiry is fixed at login time + `session_ttl` (default `24h`), not extended by activity.

### GET /login

Serves the HTML login form. Redirects (`302`) to `/` if the request already carries a valid session cookie. The `error` query parameter is shown above the form (HTML-escaped); the username field is pre-filled with the configured username.

### POST /auth/login

Form-encoded (`application/x-www-form-urlencoded`), not JSON.

| Field | Required | Description |
|-------|----------|-------------|
| `username` | yes | Must equal the configured username |
| `password` | yes | Verified against the argon2id hash in `dumpstore.conf` |

Always answers with a `302` redirect:

| Outcome | Redirect | Cookie |
|---------|----------|--------|
| Success | `/` | `dumpstore_session=<64 hex chars>`; `Path=/`, `HttpOnly`, `SameSite=Strict`, `Max-Age=<session_ttl>`, `Secure` when served over HTTPS |
| Wrong credentials, or no password configured | `/login?error=Invalid+username+or+password.` | — |
| Rate limited | `/login?error=Too+many+attempts.+Try+again+later.` | — |

Rate limit: 10 attempts per source IP per 60 s window, counting successful attempts too. Behind a reverse proxy all clients share the proxy's IP.

### POST /auth/logout

Deletes the session server-side, expires the cookie (`Max-Age=-1`), and redirects (`302`) to `/login`. Works without a session too.

### GET /api/whoami

```json
{ "user": "admin" }
```

`user` is the `X-Remote-User` header when present, otherwise the configured username (for a session cookie). `401 {"error":"unauthorized"}` without either.

### GET /api/auth/config

```json
{ "username": "admin", "session_ttl": "24h0m0s" }
```

`session_ttl` uses Go duration formatting. The password hash, trusted proxies and TLS fields are not returned.

### POST /api/auth/change-password

```json
{ "current_password": "old-secret", "new_password": "new-secret" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `current_password` | yes | Must match the stored hash |
| `new_password` | yes | Non-empty; no other strength rules |

The new password is hashed (argon2id) in Go and written to `password_hash` in `dumpstore.conf` by `auth_set_password.yml`. The new hash is used for the next login straight away; existing sessions stay valid. Returns Ansible task steps.

`400` invalid body or empty `new_password` · `401 "current password is incorrect"` (also when no password is configured) · `500` hashing or playbook failure.

### POST /api/auth/change-username

```json
{ "username": "storage-admin" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `username` | yes | `^[a-zA-Z][a-zA-Z0-9._-]{0,62}$` — starts with a letter, max 63 chars |

Does not ask for the current password. Writes `username` in `dumpstore.conf` via `auth_set_username.yml`, then **invalidates all sessions**, including the caller's, so everyone has to log in again. Returns Ansible task steps.

`400` invalid body or username · `500` playbook failure.

---

## System

### GET /api/sysinfo

Host and process information. The installed-software probes and platform warnings run about 13 external binaries, so they are cached for 5 minutes. Everything else is read on each request.

```json
{
  "app_version": "v0.9.0",
  "otel": { "enabled": false },
  "hostname": "nas",
  "os": "linux",
  "arch": "amd64",
  "kernel": "6.8.0-45-generic",
  "cpu_count": 8,
  "uptime_secs": 864213.4,
  "load1": 0.52,
  "load5": 0.58,
  "load15": 0.59,
  "pid": 1234,
  "proc_uptime_secs": 3600.2,
  "heap_alloc_mb": 6.1,
  "sys_mb": 22.4,
  "goroutines": 41,
  "num_gc": 87,
  "software": [
    { "name": "ZFS", "version": "zfs-2.2.3-1", "required": true },
    { "name": "smartctl", "version": "smartctl 7.4 2023-08-01 r5530 …", "required": false, "feature": "SMART drive health" },
    { "name": "lego (ACME)", "version": "", "required": false, "feature": "TLS via ACME (Let's Encrypt)" }
  ]
}
```

| Field | Notes |
|-------|-------|
| `otel` | `{"enabled": false}`, or, when an `OTEL_EXPORTER_OTLP_*` endpoint is set, `enabled`, `endpoint`, `protocol` (default `http/protobuf`), `service_name` |
| `software[].version` | First output line of the tool's version command, `"installed"` for tools probed only by presence, `""` when missing |
| `warnings` | FreeBSD only, omitted when empty: ZFS kernel module not loaded, `zfs_enable` not `YES` in rc.conf, zfs-auto-snapshot installed without a cron entry |

### GET /api/version

```json
{ "version": "zfs-2.2.3-1" }
```

First line of `zpool version`. If the command fails, the response is still `200` with `"version": ""`.

### GET /api/network

All network interfaces (Go `net.Interfaces()`).

```json
[
  {
    "name": "eno1",
    "state": "up",
    "mac": "3c:ec:ef:12:34:56",
    "mtu": 1500,
    "addrs": ["192.168.1.10/24", "fe80::3eec:efff:fe12:3456/64"],
    "speed_mbps": 1000,
    "rx_bytes": 123456789,
    "tx_bytes": 98765432,
    "virtual": false
  }
]
```

| Field | Notes |
|-------|-------|
| `state` | `up` or `down` (interface flag, not link carrier) |
| `speed_mbps` | `-1` if unknown. Linux: `/sys/class/net/<if>/speed`; FreeBSD: parsed from `ifconfig -a` `media:` line |
| `rx_bytes`, `tx_bytes` | Linux only (`/sys/class/net/<if>/statistics`); always `0` on FreeBSD |
| `virtual` | Loopback, or name starts with `docker`, `virbr`, `veth`, `br-`, `tun`, `tap` |

`500` if the interfaces cannot be listed.

### GET /api/schema

Dataset property definitions filtered for the running OS, plus host metadata the UI needs to build its forms.

```json
{
  "os": "linux",
  "dataset_properties": [
    {
      "name": "compression",
      "label": "Compression",
      "input_type": "select",
      "editable": true,
      "create": true,
      "applies_to": ["filesystem", "volume"],
      "options": [
        { "value": "", "label": "— inherit —" },
        { "value": "lz4", "label": "lz4", "default": true },
        { "value": "zstd", "label": "zstd" }
      ]
    }
  ],
  "user_shells": ["/bin/sh", "/bin/bash", "/usr/sbin/nologin"],
  "capabilities": { "rewrite": true, "draid": true }
}
```

| Field | Notes |
|-------|-------|
| `dataset_properties[].input_type` | `select` or `text` |
| `dataset_properties[].editable` / `create` | Shown in the edit / create dialog |
| `user_shells` | Non-comment lines of `/etc/shells`; fallback `["/bin/bash","/bin/sh","/sbin/nologin","/usr/sbin/nologin"]` if unreadable |
| `capabilities.rewrite` | `zfs rewrite` subcommand present (gates `POST /api/rewrite/{name}`) |
| `capabilities.draid` | `draid` listed in `zpool upgrade -v` |

Capabilities are probed once per process; reinstalling ZFS requires a restart to update them. The same property schema is written to `playbooks/vars/zfs_allowed_values.yml` at startup for playbook validation.

### GET /api/v1/openapi.json

The OpenAPI 3.1 document for every route, generated on first request from the server's route registry (`internal/apispec`); identical to the committed `api/openapi.json` of the same version. Requires authentication like the rest of `/api`. Each operation carries `operationId`, a tag, `x-fern-sdk-group-name` and `x-fern-sdk-method-name` (SDK generation, e.g. Forge), and is marked `x-forge-internal` when it is not meant for SDKs (SSE, `/metrics`, the HTML login page, this document).

### GET /metrics

Prometheus text exposition (`text/plain; version=0.0.4`). Unauthenticated by default (`unprotected_paths: ["/metrics"]`).

| Metric | Type | Labels |
|--------|------|--------|
| `process_start_time_seconds` | gauge | — |
| `go_goroutines`, `go_memstats_*`, `go_gc_cycles_total`, `go_gc_pause_ns_total` | gauge / counter | — |
| `dumpstore_build_info` | gauge (`1`) | `version`, `goversion` |
| `http_requests_total` | counter | `method`, `path` (normalised, e.g. `/api/datasets/{name}`), `status` |
| `http_request_duration_seconds` | histogram | `method`, `path` |
| `ansible_runs_total` | counter | `playbook`, `status` |
| `ansible_run_duration_seconds` | histogram | `playbook` |
| `dumpstore_op_runs_total` | counter | `op`, `status` (in-process single-command writes) |
| `dumpstore_op_duration_seconds` | histogram | `op` |

No pool, dataset or SMART metrics are exported. Only `/api/*` and `/metrics` requests are counted in the HTTP metrics; static files are not.

### GET /api/smart

S.M.A.R.T. summary for every device found by `smartctl --scan`, read with `smartctl -j -a <dev>`.

```json
{
  "available": true,
  "drives": [
    {
      "device": "/dev/sda",
      "model": "WDC WD40EFRX-68N32N0",
      "serial": "WD-WCC7K1234567",
      "protocol": "ATA",
      "capacity_bytes": 4000787030016,
      "temp_c": 34,
      "power_on_hours": 21873,
      "passed": true,
      "reallocated_sectors": 0,
      "pending_sectors": 0,
      "uncorrectable_errors": 0,
      "grown_defects": 0,
      "media_errors": 0
    }
  ]
}
```

| Field | Notes |
|-------|-------|
| `available` | `false` when `smartctl` is not in `PATH` (`drives` is then `null`) |
| `protocol` | `ATA`, `SCSI`, or `NVMe` |
| `temp_c`, `capacity_bytes` | `0` if unknown |
| `reallocated_sectors` / `pending_sectors` / `uncorrectable_errors` | ATA attributes 5 / 197 / 198 (raw value) |
| `grown_defects` | SCSI only |
| `media_errors` | NVMe only |

Always `200`. Drives that return no output or invalid JSON are left out. Each call queries every drive one after another, with no timeout.

### GET /api/iostat

One fresh sample per pool from `zpool iostat -H -p -y 1 1`, so the request blocks for about 1 s.

```json
[
  { "pool": "tank", "read_ops": 12, "write_ops": 443, "read_bw": 1572864, "write_bw": 28311552 }
]
```

`read_bw` / `write_bw` are bytes per second; ops are per second. `500` if `zpool iostat` fails (30 s timeout). Also pushed on the `iostat` SSE topic.

---

## Pools (read)

### GET /api/pools

From `zpool list -H -p -o name,size,alloc,free,health,frag,cap,dedup`.

```json
[
  {
    "name": "tank",
    "size": 3985729650688,
    "alloc": 1288490188800,
    "free": 2697239461888,
    "health": "ONLINE",
    "frag": "4",
    "cap": "32",
    "dedup": "1.00",
    "used_percent": 32.33
  }
]
```

`size` / `alloc` / `free` are bytes. `frag`, `cap`, `dedup` are the raw `zpool list -p` strings (`frag` is `-` when not applicable). `used_percent` is `alloc / size × 100`, computed server-side. Empty array if there are no pools; `500` if `zpool list` fails. Also pushed on the `pool.query` SSE topic.

### GET /api/poolstatus

Parsed `zpool status` for every pool.

```json
[
  {
    "name": "tank",
    "state": "ONLINE",
    "status": "Some supported and requested features are not enabled on the pool.",
    "scan": "scrub repaired 0B in 01:12:44 with 0 errors on Sun Nov  8 01:36:45 2026",
    "errors": "No known data errors",
    "vdevs": [
      { "name": "tank",     "state": "ONLINE", "read": 0, "write": 0, "cksum": 0, "depth": 0 },
      { "name": "mirror-0", "state": "ONLINE", "read": 0, "write": 0, "cksum": 0, "depth": 1 },
      { "name": "sda",      "state": "ONLINE", "read": 0, "write": 0, "cksum": 0, "depth": 2 },
      { "name": "sdb",      "state": "ONLINE", "read": 0, "write": 0, "cksum": 0, "depth": 2 }
    ]
  }
]
```

| Field | Notes |
|-------|-------|
| `status` | First line of the advisory message only; omitted when absent |
| `scan` | Scan/scrub/resilver text; continuation lines joined with `\n`. The UI derives the scrub state (in progress / paused / idle) from it |
| `vdevs` | Flat list in `zpool status` order; `depth` (indent ÷ 2) encodes the tree. Section headers such as `logs`, `cache`, `spares` appear as entries with empty `state` |

`500` if `zpool status` fails. Also pushed on the `poolstatus` SSE topic.

---

## Datasets

### GET /api/datasets

Lists every filesystem and volume. Runs `zfs list -H -p -o name,used,avail,refer,mountpoint,type,compressratio,compression,quota,reservation,sharenfs,sharesmb,acltype`. Sizes are in bytes.

```json
[
  {
    "name": "tank/data",
    "used": 10737418240,
    "avail": 53687091200,
    "refer": 10737418240,
    "mountpoint": "/tank/data",
    "type": "filesystem",
    "compress_ratio": "1.42x",
    "compression": "lz4",
    "quota": 0,
    "reservation": 0,
    "pool": "tank",
    "depth": 1,
    "sharenfs": "off",
    "sharesmb": "off",
    "acltype": "posix"
  }
]
```

- `pool` and `depth` are derived from the name: the first path component, and the number of `/` characters.
- `quota: 0` and `reservation: 0` mean none.
- Volumes report `mountpoint: "-"`.
- `acltype` is normalised to `posix`, `nfsv4` or `off`.

The same payload is pushed on the `dataset.query` SSE topic.

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

A `mountpoint` change is subject to the [reference guard](#reference-guard-409).

Editable properties: `compression`, `quota`, `mountpoint`, `recordsize`, `atime`, `exec`, `sync`, `dedup`, `copies`, `xattr`, `readonly`, `acltype`, `sharenfs`, `sharesmb`.

### GET /api/dataset-props/{name}

Returns the editable properties of one dataset, each with its value and source. Runs `zfs get -H` for `compression, quota, mountpoint, recordsize, atime, exec, sync, dedup, copies, xattr, readonly, acltype, sharenfs, sharesmb`.

```json
{
  "compression": { "value": "lz4",        "source": "inherited from tank" },
  "quota":       { "value": "50G",        "source": "local" },
  "mountpoint":  { "value": "/tank/data", "source": "default" },
  "recordsize":  { "value": "128K",       "source": "default" },
  "readonly":    { "value": "off",        "source": "default" }
}
```

- Values are human-readable (no `-p`).
- On volumes, filesystem-only properties come back as `"-"` / `"-"`.
- Returns `400` if the name is invalid.
- Returns `500` if `zfs get` fails, which includes a dataset that does not exist.

### POST /api/datasets/rename

Renames a dataset or volume within its current parent.

```json
{ "name": "tank/data", "new_name": "tank/archive" }
```

Validation (`400`):
- both names are required and must be valid ZFS names
- neither name may be a pool root (both must contain `/`)
- both names must have the same parent. Moving to another parent is refused.

Other status codes:
- `404` if `name` does not exist.
- `409` from the [reference guard](#reference-guard-409). The check covers the dataset and all of its descendants.

Runs `zfs_dataset_rename.yml` (`zfs rename <name> <new_name>`) and publishes `dataset.query` and `snapshot.query`.

```json
{ "name": "tank/data", "new_name": "tank/archive", "tasks": [ { "name": "Rename dataset", "status": "changed" } ] }
```

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

### GET /api/userspace/{name}

Per-user or per-group space consumption and quotas for a dataset, via `zfs userspace -Hp` / `zfs groupspace -Hp`. Query parameter `kind` is `user` (default) or `group`.

```json
{
  "dataset": "tank/data",
  "kind": "user",
  "rows": [
    { "name": "alice", "used": 1073741824, "quota": 10737418240 },
    { "name": "root", "used": 53248, "quota": 0 }
  ]
}
```

`quota: 0` means no quota is set.

### POST /api/userquota/{name}

Set or remove a per-user/per-group quota (`userquota@<principal>` / `groupquota@<principal>`).

```json
{ "kind": "user", "principal": "alice", "quota": "10G" }
```

`"quota": "none"` removes the limit. Returns Ansible task steps.

### DELETE /api/datasets/{name}

Destroy a dataset or volume. Append `?recursive=true` to also destroy all child datasets and snapshots.

Pool roots (e.g. `tank`) cannot be deleted via this endpoint — use `zpool destroy`.

#### Reference guard (409)

Destroy, rename (`POST /api/datasets/rename`), and a `mountpoint` change (`PATCH /api/datasets/{name}`) are refused with `409 Conflict` while dumpstore-managed configuration still refers to an affected dataset — for a recursive destroy or a rename, that includes every descendant. Nothing is changed. Checked references:

| `kind` | Blocks when |
|--------|-------------|
| `smb_usershare` | a usershare's path lies on the dataset (longest mountpoint match) |
| `smb_homes` | the `[homes]` base path (up to the first `%` substitution) lies on it |
| `smb_timemachine` | a Time Machine share's path lies on it |
| `iscsi_target` | an iSCSI target exports the zvol |
| `replication_source` | a replication task uses it as its exact source |
| `replication_target` | a replication task with a local target receives into it |

Path-based references only block when the path actually disappears or moves: a child with its own `mountpoint` is unaffected by a rename of its parent.

```json
{
  "error": "cannot destroy tank/data: still in use by dumpstore-managed configuration:\n  - SMB share \"data\" (/tank/data)\nRemove these references first, then retry.",
  "conflicts": [
    { "kind": "smb_usershare", "dataset": "tank/data", "name": "data", "path": "/tank/data",
      "describe": "SMB share \"data\" (/tank/data)" }
  ]
}
```

Replication conflicts also carry the task `id`. If the references cannot be read (e.g. `smb.conf` unreadable), the request fails closed with `500` and an error starting `reference check failed:`.

---

## Snapshots

### GET /api/snapshots

Lists all snapshots. Runs `zfs list -H -p -t snapshot -o name,used,refer,creation,clones`.

```json
[
  {
    "name": "tank/data@2026-05-06",
    "dataset": "tank/data",
    "snap_label": "2026-05-06",
    "used": 1048576,
    "refer": 10737418240,
    "creation": 1778073693,
    "clones": ""
  }
]
```

- `creation` is a Unix timestamp in seconds.
- `clones` is the raw comma-separated `clones` property. It is `""` when the snapshot has no clones (`zfs list -p` prints an empty field).

The same payload is pushed on the `snapshot.query` SSE topic.

### POST /api/snapshots

```json
{
  "dataset": "tank/data",
  "snapname": "2024-01-15_backup",
  "recursive": false
}
```

### POST /api/snapshots/clone

Clones a snapshot into a new writable dataset. Runs in-process as `zfs clone <snapshot> <target>`.

```json
{ "snapshot": "tank/data@2026-05-06", "target": "tank/data-restore" }
```

Validation (`400`):
- both fields are required
- `snapshot` must be `dataset@label` with a valid dataset name and label
- `target` must be a valid ZFS name that contains `/`

The snapshot is not checked for existence beforehand. If it is missing, or `target` already exists or is in a different pool, `zfs clone` fails and the request returns `500` with `tasks`.

On success, returns `201` and publishes `dataset.query` and `snapshot.query`.

```json
{ "snapshot": "tank/data@2026-05-06", "target": "tank/data-restore", "tasks": [ { "name": "Clone tank/data@2026-05-06 to tank/data-restore", "status": "changed" } ] }
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

### DELETE /api/snapshots/{dataset}@{snapname}

Destroys one snapshot. Runs in-process as `zfs destroy [-r] <dataset>@<snapname>`.

- `?recursive=true` passes `-r`. This also destroys the snapshot with the **same name in every descendant dataset**. It does not destroy clones: dependent clones make the destroy fail.
- Returns `400` if the path is not a valid `dataset@label`.
- Returns `404` if the snapshot does not exist.
- Does **not** release `dumpstore-repl` holds. A held replication snapshot fails with "dataset is busy". Use `POST /api/snapshots/delete-batch`, which releases those holds first.

```json
{ "snapshot": "tank/data@2026-05-06", "tasks": [ { "name": "Destroy snapshot tank/data@2026-05-06", "status": "changed" } ] }
```

### POST /api/snapshots/delete-batch

Destroys up to 100 snapshots in one request.

```json
{ "snapshots": ["tank/data@2026-05-05", "tank/home@2026-05-05"] }
```

- Every entry must be a valid `dataset@label`. If any entry is invalid, the request returns `400` and nothing is destroyed.
- An empty list returns `400`, and so does a list of more than 100.
- Any `dumpstore-repl` holds on the listed snapshots are released first, so replication snapshots can be deleted.
- Each snapshot gets its own step, `zfs destroy <snap>`, run in-process.
- One failure does not stop the remaining steps. If any step failed, the response is `500` with an aggregate error (`"N of M step(s) failed: …"`) and every step in `tasks`.

```json
{ "snapshots": ["tank/data@2026-05-05", "tank/home@2026-05-05"], "tasks": [ { "name": "Destroy snapshot tank/data@2026-05-05", "status": "changed" }, { "name": "Destroy snapshot tank/home@2026-05-05", "status": "changed" } ] }
```

---

## Operations

Every write request (`POST`, `PUT`, `PATCH`, `DELETE` under `/api/`) is tracked as an **operation** (#184). This is what lets several tabs or clients run writes at the same time without seeing each other's progress, and lets a client recover the outcome of a write whose connection dropped.

**Request header `X-Operation-ID`** (optional) — the client's ID for this write: 8–64 characters of `[A-Za-z0-9-]`, unique per request. Choosing it client-side lets the client filter live progress before the response arrives. Without the header the server generates one. The ID is always returned in the `X-Operation-ID` response header.

| Case | Response |
|------|----------|
| Invalid ID | `400` |
| ID already used (running or retained) | `409 {"error":"operation ID already in use","duplicate_operation":true}` — the request is **not** executed. Browsers may retransmit a write after a connection reset; treat this as "the original request arrived" and fetch its result. |

Live steps of the operation are published on the `ansible.progress` SSE topic as `{"op":"<id>","seq":N,"name":…,"status":…,"msg":…}` with `seq` counting 1, 2, 3, … per operation. A gap in `seq` means events were missed (SSE reconnect, slow subscriber); fetch them with `?after=`.

### GET /api/operations/{id}

```json
{
  "id": "3f9c0a…",
  "state": "succeeded",
  "started": "2026-11-09T19:02:11Z",
  "finished": "2026-11-09T19:02:12Z",
  "http_status": 201,
  "steps": [{"op":"3f9c0a…","seq":1,"name":"Create snapshot tank@bkp","status":"changed"}],
  "result": {"snapshot":"tank@bkp","tasks":[…]}
}
```

| Field | Notes |
|-------|-------|
| `state` | `running`, `succeeded` (final status < 400), or `failed` |
| `steps` | Steps with `seq` greater than the `after` query parameter (default `0` = all) |
| `http_status`, `result` | The final HTTP status and JSON response body, exactly as the original request returned them; absent while running. `result` is omitted (`result_truncated: true`) when the body exceeded 256 KiB. |

Returns `404 operation not found or expired` for unknown IDs. Records are held in memory only (lost on restart): running operations always, finished ones for up to 1 hour and at most 200.

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

---

## Pool lifecycle

### POST /api/pools

Create a new pool via `zpool create`. **Destroys all existing data on the selected devices** — the UI gates this behind confirm-by-typing.

```json
{
  "name": "tank",
  "vdev_type": "mirror",
  "devices": ["/dev/sdb", "/dev/sdc"],
  "ashift": "12",
  "compression": "zstd"
}
```

`vdev_type`: `single` (stripe, no redundancy keyword), `mirror`, `raidz1`, `raidz2`, `raidz3`, `draid`, `draid2`, `draid3`. Minimum device counts are enforced (mirror ≥ 2, raidzN ≥ N+1, draidN ≥ N+1). `ashift` (9–16) and `compression` are optional. Returns `409` if a pool with that name already exists. Returns task steps (runs in-process, no playbook).

### GET /api/pools/importable

Scans with `zpool import` (no pool name) and returns pools available for import.

```json
[
  { "name": "tank", "id": "11536261005894085344", "state": "ONLINE" },
  { "name": "backup", "id": "9183745610293847561", "state": "DEGRADED", "status": "One or more devices are missing from the system." }
]
```

### POST /api/pools/import

```json
{ "pool": "tank", "force": false }
```

`force: true` passes `-f` — needed for pools that were not cleanly exported or belonged to another system. Returns task steps (runs in-process, no playbook).

### POST /api/pools/{pool}/export

Export the pool via `zpool export`. Fails when the pool is busy (open files, active shares, running jobs) — the error is surfaced in the op-log. Returns task steps (runs in-process, no playbook).

### POST /api/pools/{pool}/vdevs

Add a data vdev via `zpool add`. **Irreversible on most pool layouts** — the UI gates this behind confirm-by-typing.

```json
{ "vdev_type": "mirror", "devices": ["/dev/sdd", "/dev/sde"] }
```

`vdev_type`: `single`, `mirror`, `raidz1`, `raidz2`, `raidz3` (minimum device counts enforced). Returns task steps (runs in-process, no playbook).

### POST /api/pools/{pool}/cache · /log · /spare

Adds auxiliary devices: an L2ARC cache, a SLOG log, or a hot spare. Runs in-process as `zpool add <pool> cache|log [mirror]|spare <devices…>`. No playbook runs.

```json
{ "devices": ["/dev/nvme0n1"] }
```

For `/log`, an optional `"mirror": true` adds the `mirror` keyword and requires at least 2 devices.

Validation (`400`):
- `pool` must be a valid pool name, with no `/`
- `devices` must contain at least one device
- each device must match `^/?[a-zA-Z0-9][a-zA-Z0-9/_.:-]*$`

If `zpool add` itself fails, the request returns `500` with `tasks`. On success, it publishes `pool.query`.

```json
{ "pool": "tank", "tasks": [ { "name": "Add log devices to tank", "status": "changed" } ] }
```

### DELETE /api/pools/{pool}/devices/{device}

Remove a cache, log, or spare device via `zpool remove`. zpool itself rejects removals that aren't legal for the given vdev. Returns task steps (runs in-process, no playbook).

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

`old_device` accepts a device name, path, or the numeric guid `zpool status` prints for missing devices. Returns task steps (runs in-process, no playbook).

### POST /api/pools/{pool}/offline

Take a device offline for maintenance via `zpool offline`. Body: `{ "device": "sdb" }`. Returns task steps (runs in-process, no playbook).

### POST /api/pools/{pool}/online

Bring an offlined device back via `zpool online`. Body: `{ "device": "sdb" }`. Returns task steps (runs in-process, no playbook).

---

## Pool scrub

### POST /api/scrub/{pool}

Start a scrub on the named pool. Returns task steps (runs in-process, no playbook).

### DELETE /api/scrub/{pool}

Cancel a running scrub on the named pool. Returns task steps (runs in-process, no playbook).

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

Manages `com.sun:auto-snapshot*` ZFS user properties per dataset. Snapshots are created and pruned either by **dumpstore's built-in scheduler** (registered at startup when no OS daemon is active, or via `POST /api/auto-snapshot/takeover`) or by the OS daemon — `zfs-auto-snapshot` (Linux) or `zfstools` (FreeBSD). Both use the same properties and the `zfs-auto-snap_<bucket>-YYYY-MM-DD-HHMM` naming, so either can prune the other's snapshots. `GET /api/auto-snapshot/status` shows which one is active.

#### Default behaviour — important

**The two schedulers select datasets differently.** dumpstore's built-in scheduler is **opt-in**: a dataset is snapshotted in a bucket only when `com.sun:auto-snapshot` is `true` (set or inherited) *and* that bucket's property is a positive keep-count. After a takeover, datasets that were only snapshotted implicitly by the daemon are no longer snapshotted until you set the properties.

`zfs-auto-snapshot` (the OS daemon) uses an **opt-out** model: any dataset where `com.sun:auto-snapshot` is **not explicitly set** is snapshotted by default. Setting the property to `false` is how you exclude a dataset.

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

### GET /api/auto-snapshot-schedules

Returns the `com.sun:auto-snapshot*` properties of every filesystem and volume, keyed by dataset name. Each property has the same `{value, source}` shape as `GET /api/auto-snapshot/{dataset}`. Inherited values are already resolved by `zfs get`. An unset property returns `"-"` for both value and source.

```json
{
  "tank": {
    "com.sun:auto-snapshot":          { "value": "false", "source": "local" },
    "com.sun:auto-snapshot:frequent": { "value": "-",     "source": "-" },
    "com.sun:auto-snapshot:hourly":   { "value": "-",     "source": "-" },
    "com.sun:auto-snapshot:daily":    { "value": "-",     "source": "-" },
    "com.sun:auto-snapshot:weekly":   { "value": "-",     "source": "-" },
    "com.sun:auto-snapshot:monthly":  { "value": "-",     "source": "-" }
  },
  "tank/data": {
    "com.sun:auto-snapshot":          { "value": "true",  "source": "local" },
    "com.sun:auto-snapshot:daily":    { "value": "7",     "source": "local" },
    "…": "…"
  }
}
```

The same payload is pushed on the `autosnapshot.query` SSE topic after every `PUT /api/auto-snapshot/{dataset}`.

### GET /api/auto-snapshot/status

Reports which component runs auto-snapshots: the OS daemon or dumpstore's built-in scheduler.

```json
{
  "os_daemon": "zfs-auto-snapshot",
  "os_daemon_active": false,
  "dumpstore_managed": true,
  "last_runs": {
    "frequent": "2026-11-10T09:45:00Z",
    "hourly":   "2026-11-10T09:00:00Z"
  }
}
```

- `os_daemon` is `zfs-auto-snapshot` on Linux and `zfstools` on FreeBSD. On any other OS it is `""`. On Linux and FreeBSD it is reported whether or not the daemon is installed.
- `os_daemon_active` on Linux is true if any `zfs-auto-snapshot-<bucket>.timer` is enabled, or if any of `/etc/cron.d/zfs-auto-snapshot` or `/etc/cron.{hourly,daily,weekly,monthly}/zfs-auto-snapshot` exists.
- `os_daemon_active` on FreeBSD is true if `daily_zfs_snapshot_enable="YES"` appears in `/etc/periodic.conf` or `/etc/periodic.conf.local`.
- `dumpstore_managed` is true when the five bucket tasks are registered with the internal scheduler. At startup they are registered automatically if the OS daemon is not active.
- `last_runs` maps a bucket name (`frequent`, `hourly`, `daily`, `weekly`, `monthly`) to the time that bucket last fired. It is omitted when no bucket has fired.

### POST /api/auto-snapshot/takeover

Disables the OS daemon, then registers dumpstore's bucket tasks. Runs `auto_snapshot_takeover.yml` with `timer_state_file=<state dir>/autosnap-disabled-timers`. The playbook:

- **Linux, systemd timers:** stops and disables the enabled `zfs-auto-snapshot-*.timer` units and records their names in `timer_state_file`.
- **Linux, cron packaging:** renames each zfs-auto-snapshot cron file to `<file>.dumpstore-disabled`.
- **FreeBSD:** sets `daily_zfs_snapshot_enable="NO"` in `/etc/periodic.conf`, and in `/etc/periodic.conf.local` if that file exists.

The request is idempotent. Bucket schedules (cron syntax):

| Bucket | Schedule |
|--------|----------|
| frequent | `*/15 * * * *` |
| hourly | `0 * * * *` |
| daily | `0 0 * * *` |
| weekly | `0 0 * * 0` |
| monthly | `0 0 1 * *` |

On success, publishes `autosnap.status` (this payload has no `last_runs`).

```json
{ "tasks": [ { "name": "Linux — stop and disable zfs-auto-snapshot systemd timers", "status": "changed" } ] }
```

Returns `500` if the autosnap runner is not initialised, if the playbook fails (with `tasks`), or if scheduler registration fails.

### POST /api/auto-snapshot/release

The inverse of takeover. It first unregisters dumpstore's bucket tasks, then runs `auto_snapshot_release.yml`. The playbook:

- **Linux, cron packaging:** renames each `<file>.dumpstore-disabled` back to `<file>`, unless the package has recreated the file in the meantime.
- **Linux, systemd timers:** re-enables and starts the timers listed in `timer_state_file`. If there is no record, it enables every installed `zfs-auto-snapshot-*.timer`. It then deletes the record.
- **FreeBSD:** sets `daily_zfs_snapshot_enable="YES"` in `/etc/periodic.conf`.

The package is never reinstalled. Publishes `autosnap.status`.

```json
{ "tasks": [ { "name": "Linux — enable and start zfs-auto-snapshot systemd timers", "status": "changed" } ] }
```

Returns `500` if the autosnap runner is not initialised or the playbook fails. The bucket tasks are already unregistered at that point.

### GET /api/auto-snapshot/{dataset}

Returns the current `com.sun:auto-snapshot*` property values and their source (local/inherited/default) for the given dataset.

```json
{
  "com.sun:auto-snapshot":          { "value": "true",  "source": "local" },
  "com.sun:auto-snapshot:frequent": { "value": "4",     "source": "local" },
  "com.sun:auto-snapshot:hourly":   { "value": "24",    "source": "local" },
  "com.sun:auto-snapshot:daily":    { "value": "7",     "source": "local" },
  "com.sun:auto-snapshot:weekly":   { "value": "4",     "source": "local" },
  "com.sun:auto-snapshot:monthly":  { "value": "-",     "source": "-" }
}
```

A `value` and `source` of `"-"` mean the property is not set anywhere in the hierarchy.

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

On FreeBSD dumpstore owns `/etc/ctl.conf` completely (#186): every write re-reads the file, changes the target list, and renders the whole file again — one portal group per listen address, discovery allowed and filtered to what each initiator may reach (`discovery-filter portal-name`), and per-target auth. Writes are gated: `POST`/`DELETE /api/iscsi-targets` return `409 "ctld not initialised — run POST /api/iscsi/init first"` until init has run. After reloading ctld the playbook checks `ctladm portlist` and ctld's listening sockets; if the new config did not take effect (e.g. a portal IP not assigned to the host), the previous file is restored, ctld reloaded, and the request fails with the reason.

### GET /api/iscsi/status

```json
{ "backend": "freebsd", "initialized": false, "conf_path": "/etc/ctl.conf", "conf_exists": true }
```

`backend` is `linux`, `freebsd`, or `""`. On Linux `initialized` is always `true` (targetcli has no init gate) and `conf_path` is empty.

### POST /api/iscsi/init

FreeBSD only (`400` on Linux, `501` without ctld). Backs up an existing `/etc/ctl.conf` once to `/etc/ctl.conf.pre-dumpstore` (never overwritten), imports the targets dumpstore can represent — zvol-backed, LUN 0, one portal, auth none or a single CHAP credential, which includes files written by earlier dumpstore versions — and renders the file from template, then enables and reloads (or starts) ctld. Everything else (other LUN types, multi-portal targets, auth groups, `chap-mutual`, global settings) is not carried over; each item is reported as a `skipped` step `Not imported: …`. Safe to call again.

```json
{ "initialized": true, "imported": 2, "skipped": 1, "tasks": [ … ] }
```

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
| `chap_password` | when chap | CHAP password; no `"`, `\`, or newlines |
| `initiators` | no | Array of allowed initiator IQNs; empty = allow all |

Returns Ansible task steps. FreeBSD: `409` before init, if the IQN already exists, or if the zvol is already exported; `400` if `/dev/zvol/<zvol>` does not exist. CHAP credentials are never returned by `GET /api/iscsi-targets`.

### DELETE /api/iscsi-targets?iqn=\<iqn\>&zvol=\<zvol\>

Remove an iSCSI target and its backstore. Both query parameters are required.

Returns Ansible task steps. FreeBSD: `409` before init; `404` if no target with that IQN exports that zvol.

---

## Services

Manage the sharing daemons dumpstore controls (Samba, NFS, iSCSI). Status reads happen directly via `systemctl`/`service` — no Ansible overhead. Mutations (start/stop/restart/enable/disable) go through playbooks with op-log.

Logical service names: `samba`, `nfs`, `iscsi`, `wsdd`.

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
    "state": "active",
    "installed": true
  }
]
```

`state` values: `active`, `inactive`, `failed`, `unknown`. `installed` is false when the unit file (Linux) or rc.d script (FreeBSD) is absent — `unit_name` is then the preferred candidate name.

### POST /api/services/{name}/{action}

Control a service. Valid actions: `start`, `stop`, `restart`, `enable`, `disable`.

Returns Ansible task steps. `409` if the service is not installed on this host.

---

## Users & Groups

Local Unix users and groups. Reads parse `/etc/passwd` and `/etc/group` directly. Writes go through playbooks, are serialised server-side, and push fresh `user.query` / `group.query` SSE events when they succeed.

**System-account protection:** modify, delete and SSH-key writes return `403` when the account's UID/GID is below `UID_MIN` from `/etc/login.defs` (default `1000`). Modify and delete also return `403` for `nobody` and `nfsnobody` (users) and for `nogroup`, `nobody` and `nfsnobody` (groups), whatever their ID. The playbooks repeat the check with a fixed `>= 1000`.

Names (user, group, members, supplementary groups) must match `^[a-zA-Z_][a-zA-Z0-9._-]{0,31}$`. Lists are comma-separated. Paths (`shell`, `home`) must match `^/[a-zA-Z0-9/_.-]+$`. Passwords are **plaintext** — the OS hashes them (`chpasswd` on Linux, `pw usermod -h 0` on FreeBSD) — and must not contain newline characters.

A playbook failure, including a failed playbook `assert`, returns `500` with `tasks`.

### GET /api/users

All entries in `/etc/passwd`, including system accounts.

```json
[
  { "username": "root",  "uid": 0,    "gid": 0,    "home": "/root",       "shell": "/bin/bash" },
  { "username": "alice", "uid": 1001, "gid": 1001, "home": "/home/alice", "shell": "/bin/bash" }
]
```

### POST /api/users

Create a local user (`user_create.yml`). The home directory is always created.

```json
{
  "username": "alice",
  "shell": "/bin/bash",
  "uid": "1001",
  "group": "",
  "groups": "wheel,backup",
  "password": "s3cret",
  "create_group": true,
  "smb_user": true
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `username` | yes | Login name |
| `shell` | no | Login shell, defaults to `/bin/bash` |
| `uid` | no | Explicit UID **as a string**; must be `>= 1000` (playbook assert). Empty = auto |
| `group` | no | Primary group name; created if it doesn't exist |
| `groups` | no | Comma-separated supplementary groups |
| `password` | no | Plaintext password; empty leaves the account without one |
| `create_group` | no | `true` creates a group named after the user and makes it the primary group (unless `group` is set) |
| `smb_user` | no | `true` also registers the user in Samba (`smbpasswd -a`) with the same password. Skipped when `password` is empty |

Returns `201`:

```json
{ "username": "alice", "tasks": [ … ] }
```

### PUT /api/users/{name}

Modify a user (`user_modify.yml`). `404` if the user doesn't exist, `403` for system and protected accounts.

```json
{
  "shell": "/bin/zsh",
  "group": "storage",
  "user_groups": "wheel,backup",
  "password": "",
  "home": "/tank/home/alice",
  "move_home": true,
  "smb_sync": false
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `shell` | no | New login shell; empty = unchanged |
| `group` | no | New primary group (must already exist); empty = unchanged |
| `user_groups` | no | Comma-separated supplementary groups. **Replaces** the current set; empty or omitted **clears all** supplementary groups |
| `password` | no | New plaintext password; empty = unchanged |
| `home` | no | New home directory; created as `0700` and owned by the user. Empty = unchanged |
| `move_home` | no | With `home`: copy the old home's contents, dotfiles included, then delete the old directory |
| `smb_sync` | no | With `password`: also update the Samba password (`smbpasswd -s`) |

```json
{ "username": "alice", "tasks": [ … ] }
```

### DELETE /api/users/{name}

Delete a user **and their home directory** (`userdel` with remove and force). `404` if the user doesn't exist, `403` for system and protected accounts. Doesn't remove the user from Samba — use [`DELETE /api/smb-users/{name}`](#delete-apismb-usersname).

```json
{ "username": "alice", "tasks": [ … ] }
```

### GET /api/users/{name}/sshkeys

Reads `<home>/.ssh/authorized_keys` and returns one entry per non-blank, non-`#` line. If the file is missing, `keys` is `[]`. `404` if the user doesn't exist.

```json
{ "keys": [ "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… alice@laptop" ] }
```

### POST /api/users/{name}/sshkeys

Add an authorized key (`user_ssh_key_add.yml`). Creates `~/.ssh` (`0700`) and `authorized_keys` (`0600`) if they are missing, owned by `<user>:<user>`. The key is appended only if that exact line isn't already present.

```json
{ "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… alice@laptop" }
```

`key` is trimmed and must be a single line starting with `ssh-`, `ecdsa-sha2-` or `sk-` (`400` otherwise). `404` if the user doesn't exist, `403` if UID < `UID_MIN`.

```json
{ "tasks": [ … ] }
```

### DELETE /api/users/{name}/sshkeys

Remove an authorized key (`user_ssh_key_remove.yml`). The key travels in the request **body**, not the query string, and must match the line in `authorized_keys` exactly (after trimming).

```json
{ "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… alice@laptop" }
```

`400` if `key` is empty, `404` if the user doesn't exist, `403` if UID < `UID_MIN`.

```json
{ "tasks": [ … ] }
```

### GET /api/groups

All entries in `/etc/group`. `members` lists only supplementary members, never users whose primary group this is. It is always an array.

```json
[
  { "name": "wheel",   "gid": 0,    "members": [ "root" ] },
  { "name": "storage", "gid": 1500, "members": [ "alice", "bob" ] }
]
```

### POST /api/groups

Create a local group (`group_create.yml`).

```json
{ "groupname": "storage", "gid": "1500" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `groupname` | yes | Group name |
| `gid` | no | Explicit GID **as a string**; must be `>= 1000` (playbook assert). Empty = auto |

Returns `201`:

```json
{ "groupname": "storage", "tasks": [ … ] }
```

### PUT /api/groups/{name}

Rename a group, change its GID, and set its member list (`group_modify.yml`). `404` if the group doesn't exist, `403` for system and protected groups.

```json
{ "new_name": "media", "gid": "1501", "members": "alice,bob" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `new_name` | no | Rename to this name (`groupmod -n` / `pw groupmod -l`); empty = keep |
| `gid` | no | New GID as a string, `>= 1000`; empty = keep |
| `members` | no | Comma-separated members. **Always applied** (`gpasswd -M` / `pw groupmod -M`): replaces the current list, and empty or omitted **removes all members**. Every name must be an existing user — otherwise `400 "unknown member(s): …"` |

`groupname` in the response is the new name if the group was renamed:

```json
{ "groupname": "media", "tasks": [ … ] }
```

### DELETE /api/groups/{name}

Delete a local group (`group_delete.yml`). `404` if the group doesn't exist, `403` for system and protected groups.

```json
{ "groupname": "storage", "tasks": [ … ] }
```

---

## Dataset ownership

Owner and group of a dataset's **mountpoint directory**. Volumes, and datasets with `mountpoint=none`, return `400 "dataset … has no mountpoint"`.

### GET /api/chown/{dataset}

```json
{ "dataset": "tank/data", "mountpoint": "/tank/data", "owner": "alice", "group": "storage" }
```

`owner` and `group` are numeric strings (e.g. `"1005"`) when the UID or GID has no name.

### POST /api/chown/{dataset}

Set the mountpoint's owner and group (`dataset_chown.yml`, which runs `chown [-R] owner:group <mountpoint>`).

```json
{ "owner": "alice", "group": "storage", "recursive": false }
```

| Field | Required | Description |
|-------|----------|-------------|
| `owner` | yes | User name |
| `group` | yes | Group name — both are required; to change only one, send the current value for the other |
| `recursive` | no | `true` applies `chown -R` to everything below the mountpoint |

`404` if the dataset doesn't exist. The response follows; a failed `chown` (e.g. unknown user) returns `500` with `tasks`.

```json
{ "dataset": "tank/data", "tasks": [ … ] }
```

---

## ACLs

### GET /api/acl-status

Map of dataset name → `true`/`false` for every mounted filesystem (volumes and datasets with mountpoint `none`/`-` are left out). Used for the ACL badge in the datasets table.

```json
{ "tank": false, "tank/data": true, "tank/media": false }
```

A dataset is `true` if `getfacl` shows more than the three base entries (or `nfs4_getfacl` shows any entry), **or** if its `acltype` is anything other than `off`. So `true` means "extended entries present or ACLs enabled", not strictly "has extra entries". When neither tool is installed, the result is based on `acltype` alone. `500` only if `zfs list` fails.

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

## SMB shares & users

dumpstore owns `smb.conf` (`/etc/samba/smb.conf` on Linux, `/usr/local/etc/smb4.conf` on FreeBSD) and renders it from a template on every config write. Per-dataset shares are Samba **usershares** (`net usershare`), stored in dumpstore's own directory (`/etc/dumpstore/smb/usershares`, or `/usr/local/etc/dumpstore/smb/usershares` on FreeBSD).

**Init gate:** `GET /api/smb-shares`, `POST`/`DELETE /api/smb-share/…`, `POST`/`DELETE /api/smb-users/{name}` (as well as homes, Time Machine and wsdd writes) return `409 "Samba not initialised — run POST /api/smb/init first"` while `smb.conf` doesn't exist.

### GET /api/smb/status

```json
{
  "initialized": true,
  "conf_path": "/etc/samba/smb.conf",
  "os": "linux",
  "conf_mtime": "2026-11-10T09:14:02Z",
  "wsdd": { "installed": true, "unit": "wsdd", "state": "active", "active": true, "enabled": true,
            "conf_path": "/etc/default/wsdd", "manageable": true, "managed": true, "workgroup": "WORKGROUP" }
}
```

| Field | Description |
|-------|-------------|
| `initialized` | `smb.conf` exists at `conf_path` |
| `conf_path` | OS-specific `smb.conf` path |
| `os` | `linux` or `freebsd` |
| `conf_mtime` | UTC mtime of `smb.conf`; omitted when the file is missing |
| `wsdd` | See [Windows Discovery (wsdd)](#windows-discovery-wsdd); omitted when `smb.conf` can't be parsed |

### POST /api/smb/init

Initialise Samba for dumpstore. Safe to call again.

1. Returns `422 "Samba not installed — run: <install command>"` if `smbd` is missing.
2. `smb_init.yml`: copies an existing `smb.conf` to `smb.conf.pre-dumpstore` once (never overwritten). On Linux it ensures the `sambashare` group exists. It creates the usershares directory (`1770 root:sambashare` on Linux, `1755 root:wheel` on FreeBSD).
3. `smb_apply.yml`: renders `smb.conf` from the template and carries over `workgroup` and `server string` from the existing file (defaults `WORKGROUP` / `Samba Server`). It creates the referenced directories, deploys the file (keeping a backup), reloads Samba (`smbd`+`nmbd`, or `samba_server` on FreeBSD), and renders the wsdd options file if wsdd is installed.

```json
{ "initialized": true, "tasks": [ … ] }
```

`tasks` holds the steps from both playbooks, in order.

### GET /api/smb-shares

Reads the usershare files directly, so the list also includes shares whose path no longer exists (`path_missing: true`). Samba hides those shares but re-activates them when the path reappears. Sorted by name; `[]` if there are none.

```json
[
  { "name": "data",  "path": "/tank/data",  "path_missing": false },
  { "name": "olds",  "path": "/tank/olds",  "path_missing": true }
]
```

### POST /api/smb-share/{dataset}

Create or update a usershare for the dataset's mountpoint (`smb_usershare_set.yml` → `net usershare add <sharename> <mountpoint> "" everyone:F guest_ok=n`). Adding the share holds the [reference guard](#reference-guard-409) lock, so the dataset can't be destroyed or moved at the same moment. Once the share exists, it blocks destroy, rename and mountpoint changes of the dataset.

```json
{ "sharename": "data" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `sharename` | yes | 1–80 chars of `[a-zA-Z0-9._-]` |

`400` for an invalid dataset name or sharename. A dataset whose mountpoint is `none` or `-` fails the playbook (`500` with `tasks`).

```json
{ "dataset": "tank/data", "sharename": "data", "tasks": [ … ] }
```

### DELETE /api/smb-share/{dataset}?name=\<sharename\>

Remove a usershare (`smb_usershare_unset.yml` → `net usershare delete <sharename>`). The `name` query parameter is required (`400` otherwise) and alone selects the share. `{dataset}` is only echoed in the response.

```json
{ "dataset": "tank/data", "sharename": "data", "tasks": [ … ] }
```

### GET /api/smb-users

Users in Samba's passdb (`pdbedit -L`). Not behind the init gate. If `pdbedit` isn't installed, the response is `200` with `available: false`.

```json
{ "available": true, "users": [ "alice", "bob" ] }
```

### POST /api/smb-users/{name}

Register an existing local user with Samba, or reset their Samba password (`smb_user_add.yml` → `smbpasswd -a -s`).

```json
{ "password": "s3cret" }
```

`password` is required and must not contain newline characters (`400`). The playbook fails (`500` with `tasks`) if `smbpasswd` is missing or the Unix user doesn't exist.

```json
{ "username": "alice", "tasks": [ … ] }
```

### DELETE /api/smb-users/{name}

Remove a user from Samba's passdb (`smb_user_remove.yml` → `pdbedit -x -u`). The Unix account isn't touched.

```json
{ "username": "alice", "tasks": [ … ] }
```

---

## SMB Home Shares

Manage the Samba `[homes]` section in `smb.conf`. When enabled, each authenticated user automatically gets a personal share mapped to a subdirectory under the configured base path.

### GET /api/smb/homes

Returns the current `[homes]` configuration. If the section is not present, `enabled` is `false` and all fields are empty.

```json
{
  "enabled": true,
  "path": "/tank/homes/%S",
  "browseable": "yes",
  "read_only": "no",
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
  "browseable": "yes",
  "read_only": "no",
  "create_mask": "0644",
  "directory_mask": "0755"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `path` | yes | Base path for home directories (may include `%S` for username substitution) |
| `dataset` | no | ZFS dataset to use as base path (alternative to specifying `path` directly) |
| `browseable` | yes | `"yes"` or `"no"` — whether the share is visible in browse lists |
| `read_only` | yes | `"yes"` or `"no"` |
| `create_mask` | no | File creation mask (default `"0644"`) |
| `directory_mask` | no | Directory creation mask (default `"0755"`) |

### DELETE /api/smb/homes

Remove the `[homes]` section from `smb.conf`. Returns Ansible task steps.

---

## Windows Discovery (wsdd)

wsdd announces the host in the Windows Network view. It advertises only the host and its workgroup, so dumpstore derives everything from `smb.conf` — there are no separate settings. `GET /api/smb/status` carries a `wsdd` object:

| Field | Description |
|-------|-------------|
| `installed` | wsdd unit (Linux) / rc.d script (FreeBSD) present |
| `install_hint` | Distro install command (only when not installed) |
| `unit`, `state`, `active`, `enabled` | Service status (`wsdd-server` on Debian/Ubuntu, else `wsdd`) |
| `conf_path` | Options file dumpstore renders — the unit's `EnvironmentFile`; `""` on FreeBSD |
| `manageable` | Linux: the unit expands `$WSDD_PARAMS`; always true on FreeBSD |
| `managed` | Options file carries dumpstore's header (FreeBSD: always true — rc.d reads `smb4.conf`) |
| `workgroup` | Workgroup announced (`""` → wsdd's default `WORKGROUP`; names with spaces/shell characters are not passed) |

On Linux every Samba config write (`smb_apply.yml`) also renders the options file and restarts wsdd only if it was running and the file changed.

### POST /api/smb/wsdd

Re-applies the current Samba config so the wsdd options file is rendered — for hosts where wsdd was installed after Samba was initialised. Returns Ansible task steps. `409` if Samba is not initialised, or wsdd is not installed / its unit does not read `$WSDD_PARAMS`.

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

## TLS / HTTPS

TLS settings live in `dumpstore.conf` (`tls_enabled`, `tls_cert_path`, `tls_key_path`), written by `tls_set_config.yml`. **No endpoint switches the running server to HTTPS.** HTTPS starts only when dumpstore is restarted with `--tls` and both cert paths are set. The certificate is read once at startup, so a restart is also needed after every gencert/issue/renew. With `--tls`, `-addr` is ignored: HTTPS listens on `--tls-port` (default `443`) and plain HTTP on `--http-port` (default `80`) answers with `301` to `https://`.

Path fields must be absolute and must not contain NUL, `;`, `|`, `&`, `$` or `` ` ``. Hostnames and domains must be DNS names (labels of letters, digits and `-`).

### GET /api/tls/status

```json
{
  "enabled": true,
  "cert_path": "/etc/dumpstore/tls/cert.pem",
  "key_path": "/etc/dumpstore/tls/key.pem",
  "cn": "nas.local",
  "sans": ["nas.local", "127.0.0.1"],
  "expires_at": "2036-11-08T10:12:00Z",
  "days_remaining": 3649,
  "self_signed": true
}
```

| Field | Notes |
|-------|-------|
| `enabled` | The `tls_enabled` config flag, **not** whether HTTPS is being served (that depends on `--tls`) |
| `cn`, `sans`, `expires_at`, `days_remaining`, `self_signed` | Parsed from the first PEM block of `cert_path`; omitted if the file cannot be read or parsed. `sans` = DNS names + IPs. `self_signed` = issuer equals subject. `days_remaining` is negative once expired and omitted when `0` |
| `acme_email`, `acme_domain` | Present only after `POST /api/tls/acme/issue` in the current process (not persisted, see below) |

All fields except `enabled` are omitted when empty.

### POST /api/tls/gencert

Generate a self-signed certificate and point the config at it.

```json
{ "hostname": "nas.local", "cert_dir": "/etc/dumpstore/tls" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `hostname` | yes | Becomes CN and DNS SAN |
| `cert_dir` | no | Default `<config dir>/tls` (e.g. `/etc/dumpstore/tls`, `/usr/local/etc/dumpstore/tls`) |

Runs `tls_gencert.yml`: creates `cert_dir` (`0700`), then `openssl req -x509` with an ECDSA P-256 key, valid 3650 days, SAN `IP:127.0.0.1,DNS:<hostname>`, written to `cert.pem` / `key.pem` (key `0600`). **If `cert.pem` already exists it is not regenerated**, even if `hostname` is different. Then `tls_set_config.yml` stores both paths and `tls_enabled: true`. Returns the Ansible task steps of both playbooks.

`400` missing/invalid `hostname` or invalid `cert_dir` · `500` playbook failure.

### PATCH /api/tls/config

Use an existing certificate + key from disk (e.g. from certbot or acme.sh).

```json
{ "cert_path": "/etc/letsencrypt/live/nas.example.com/fullchain.pem",
  "key_path":  "/etc/letsencrypt/live/nas.example.com/privkey.pem" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `cert_path` | yes | PEM certificate (chain) |
| `key_path` | yes | PEM private key matching the certificate |

Before anything is written, the pair is loaded in Go (`tls.LoadX509KeyPair`), so files that are missing, unreadable or don't match are rejected. Then `tls_set_config.yml` stores the paths and `tls_enabled: true`. Files are not copied. Returns Ansible task steps.

`400` missing/invalid path or `invalid cert/key pair: …` · `500` playbook failure.

### POST /api/tls/acme/issue

Issue a Let's Encrypt certificate with [`lego`](https://go-acme.github.io/lego/).

```json
{ "email": "admin@example.com", "domain": "nas.example.com", "cert_dir": "/etc/dumpstore/tls/acme" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `email` | yes | ACME account email |
| `domain` | yes | Single domain; no wildcards |
| `cert_dir` | no | lego `--path`; default `<config dir>/tls/acme` |

Runs `tls_acme_issue.yml`: checks that `lego` is installed, creates `cert_dir` (`0700`), runs `lego --email … --domains … --path … --tls run`. This is the **TLS-ALPN-01** challenge: Let's Encrypt must reach this host on port 443, and nothing (including dumpstore itself with `--tls`) may be listening on 443. Then `tls_set_config.yml` stores `<cert_dir>/certificates/<domain>.crt` / `.key` and `tls_enabled: true`. Returns the Ansible task steps of both playbooks.

`email`/`domain` are kept **in memory only**, not in `dumpstore.conf`. After a restart, renew no longer knows them (see below).

`400` missing/invalid `email`, `domain` or `cert_dir` · `500` lego missing, challenge failed, or playbook failure.

### POST /api/tls/acme/renew

No request body. Runs `tls_acme_renew.yml` → `lego --email … --domains … --path <cert_dir> --tls renew --days 30` using the email/domain from the last issue. lego renews only when the certificate expires within 30 days; otherwise the call succeeds without changing anything. `cert_dir` is derived from the configured cert path (two levels up from `certificates/<domain>.crt`).

**Renewal is manual.** dumpstore has no renewal timer. Same port-443 constraint as issue, so this fails while dumpstore itself serves HTTPS on 443. Restart afterwards to load the renewed certificate. Returns Ansible task steps.

`400 "ACME is not configured"` if no issue has run since the process started (includes every restart after an issue) · `400 "ACME email and domain are not set"` · `500` lego missing or renewal failed.

---

## Server-Sent Events

### GET /api/events

Subscribe to live data updates. The server pushes named events whenever data changes.

**Query parameter:** `topics` — comma-separated list of topics to subscribe to.

**Available topics:**

| Topic                | Data                                            | Cadence |
|----------------------|-------------------------------------------------|---------|
| `pool.query`         | Same JSON as `GET /api/pools`                   | Every 10 s on change |
| `poolstatus`         | Same JSON as `GET /api/poolstatus`              | Every 10 s on change |
| `dataset.query`      | Same JSON as `GET /api/datasets`                | Every 10 s on change |
| `snapshot.query`     | Same JSON as `GET /api/snapshots`               | Every 10 s on change |
| `autosnapshot.query` | Same JSON as `GET /api/auto-snapshot-schedules` | Every 10 s on change |
| `iostat`             | Same JSON as `GET /api/iostat`                  | Every 10 s |
| `user.query`         | Same JSON as `GET /api/users`                   | Every 10 s on change + after writes |
| `group.query`        | Same JSON as `GET /api/groups`                  | Every 10 s on change + after writes |
| `service.query`      | Same JSON as `GET /api/services`                | Every 10 s on change |
| `jobs.update`        | Single job object (as `GET /api/jobs/{id}`)     | On every job state change |
| `replication.update` | Same JSON as `GET /api/replication`             | On task create/update/delete + after each scheduled run |
| `autosnap.status`    | Same JSON as `GET /api/auto-snapshot/status`    | After takeover / release |
| `ansible.progress`   | Single step `{op, seq, name, status, msg}` (see [Operations](#operations)) | Streamed during every write (Ansible and ops lanes); not cached |

Unknown topic names are ignored. The browser UI subscribes to all of them and falls back to 30 s REST polling (retrying SSE after 5 s) if the stream drops.

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
