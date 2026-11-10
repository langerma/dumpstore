# API Reference

All endpoints are served at `http://<host>:8080`. The API is JSON over HTTP. The exceptions are the login form (`POST /auth/login`, form-encoded), the event stream (`GET /api/v1/events`, `text/event-stream`) and `GET /metrics` (Prometheus text).

## Endpoints

The complete endpoint list is the OpenAPI document, generated from the server's route registry, so it always matches the running version:

- served by dumpstore at `GET /api/v1/openapi.json` (authenticated)
- committed in the repository as [`api/openapi.json`](https://github.com/langerma/dumpstore/blob/main/api/openapi.json); CI fails when it is stale

It lists every operation with its method and path, parameters, request and response schemas, documented error statuses, and SDK names (`operationId`; Forge's `x-forge-group-name` / `x-forge-method-name` and Fern's `x-fern-sdk-*`). CI generates and type-checks a TypeScript SDK from it with [Forge](https://github.com/cloudflare/forge). Generated SDKs percent-encode names in paths themselves. To browse it:

```bash
# a self-contained HTML reference (Redocly CLI 2.x)
npx @redocly/cli build-docs api/openapi.json -o api-reference.html
```

Any OpenAPI 3.1 tool works as well (Swagger UI, Stoplight, client generators).

Conventions that apply to every operation:

- **Paths** live under `/api/v1`. Dataset, snapshot and device names contain `/` and `@`: percent-encode them as one path segment (`/api/v1/datasets/tank%2Fdata/acl`). Reverse proxies must forward `%2F` undecoded, see [[Installation]].
- **Writes** (`POST`, `PUT`, `PATCH`, `DELETE`) accept an optional `X-Operation-ID` header and return `tasks` (the op-log steps); see [Operations](#operations) below.
- **Errors** share one body, `{"error": "…"}`, plus `tasks` for a failed write and `conflicts` for the reference guard's `409`.

The sections below describe behaviour the schema can't express: validation rules, what runs on the host, side effects and SSE topics. Where they and the spec disagree, the spec is right; please report it.

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

### GET /api/v1/auth/whoami

```json
{ "user": "admin" }
```

`user` is the `X-Remote-User` header when the request comes from a `trusted_proxies` CIDR, otherwise the configured username for a valid session cookie. The header is ignored from any other address. `401 {"error":"unauthorized"}` without either. The auth middleware uses the same rule.

### GET /api/v1/auth/config

```json
{ "username": "admin", "session_ttl": "24h0m0s" }
```

`session_ttl` uses Go duration formatting. The password hash, trusted proxies and TLS fields are not returned.

### PUT /api/v1/auth/password

```json
{ "current_password": "old-secret", "new_password": "new-secret" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `current_password` | yes | Must match the stored hash |
| `new_password` | yes | Non-empty; no other strength rules |

The new password is hashed (argon2id) in Go and written to `password_hash` in `dumpstore.conf` by `auth_set_password.yml`. The new hash is used for the next login straight away; existing sessions stay valid. Returns Ansible task steps.

`400` invalid body or empty `new_password` · `401 "current password is incorrect"` (also when no password is configured) · `500` hashing or playbook failure.

### PUT /api/v1/auth/username

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

### GET /api/v1/system/info

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

### GET /api/v1/system/version

```json
{ "version": "zfs-2.2.3-1" }
```

First line of `zpool version`. If the command fails, the response is still `200` with `"version": ""`.

### GET /api/v1/system/network

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

### GET /api/v1/system/schema

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
| `capabilities.rewrite` | `zfs rewrite` subcommand present (gates `POST /api/v1/datasets/{name}/rewrite`) |
| `capabilities.draid` | `draid` listed in `zpool upgrade -v` |

Capabilities are probed once per process; reinstalling ZFS requires a restart to update them. The same property schema is written to `playbooks/vars/zfs_allowed_values.yml` at startup for playbook validation.

### GET /api/v1/openapi.json

The OpenAPI 3.1 document for every route, generated on first request from the server's route registry (`internal/apispec`); identical to the committed `api/openapi.json` of the same version. Requires authentication like the rest of `/api`. Each operation carries `operationId`, a tag, and its SDK group and method as `x-forge-group-name` / `x-forge-method-name` (Forge) and `x-fern-sdk-group-name` / `x-fern-sdk-method-name` (Fern). Operations that are not meant for SDKs (SSE, `/metrics`, the HTML login page, this document) are marked `x-forge-skip` and `x-fern-ignore`.

### GET /metrics

Prometheus text exposition (`text/plain; version=0.0.4`). Unauthenticated by default (`unprotected_paths: ["/metrics"]`).

| Metric | Type | Labels |
|--------|------|--------|
| `process_start_time_seconds` | gauge | — |
| `go_goroutines`, `go_memstats_*`, `go_gc_cycles_total`, `go_gc_pause_ns_total` | gauge / counter | — |
| `dumpstore_build_info` | gauge (`1`) | `version`, `goversion` |
| `http_requests_total` | counter | `method`, `path` (route pattern, e.g. `/api/v1/datasets/{name}`; `(unauthenticated)` / `(unmatched)` for requests that reached no route), `status` |
| `http_request_duration_seconds` | histogram | `method`, `path` |
| `ansible_runs_total` | counter | `playbook`, `status` |
| `ansible_run_duration_seconds` | histogram | `playbook` |
| `dumpstore_op_runs_total` | counter | `op`, `status` (in-process single-command writes) |
| `dumpstore_op_duration_seconds` | histogram | `op` |

No pool, dataset or SMART metrics are exported. Only `/api/*` and `/metrics` requests are counted in the HTTP metrics; static files are not.

### GET /api/v1/system/smart

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

### GET /api/v1/pools/iostat

One fresh sample per pool from `zpool iostat -H -p -y 1 1`, so the request blocks for about 1 s.

```json
[
  { "pool": "tank", "read_ops": 12, "write_ops": 443, "read_bw": 1572864, "write_bw": 28311552 }
]
```

`read_bw` / `write_bw` are bytes per second; ops are per second. `500` if `zpool iostat` fails (30 s timeout). Also pushed on the `iostat` SSE topic.

---

## Pools (read)

### GET /api/v1/pools

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

### GET /api/v1/pools/status

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

### GET /api/v1/datasets

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

### POST /api/v1/datasets

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

### PATCH /api/v1/datasets/{name}

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

### GET /api/v1/datasets/{name}/properties

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

### POST /api/v1/datasets/{name}/rename

Renames a dataset or volume within its current parent. The current name is the (percent-encoded) path parameter, e.g. `POST /api/v1/datasets/tank%2Fdata/rename`.

```json
{ "new_name": "tank/archive" }
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

### POST /api/v1/datasets/{name}/rewrite

Rewrite the existing blocks of a mounted filesystem via `zfs rewrite` so that updated properties (compression, checksum, dedup, copies) apply to already-stored data. Runs as a **background job** — returns `202 Accepted` with a `job_id`; progress and output appear in the Jobs tab (`GET /api/v1/jobs`).

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

### GET /api/v1/datasets/{name}/userspace

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

### PUT /api/v1/datasets/{name}/quotas

Set or remove a per-user/per-group quota (`userquota@<principal>` / `groupquota@<principal>`).

```json
{ "kind": "user", "principal": "alice", "quota": "10G" }
```

`"quota": "none"` removes the limit. Returns Ansible task steps.

### DELETE /api/v1/datasets/{name}

Destroy a dataset or volume. Append `?recursive=true` to also destroy all child datasets and snapshots.

Pool roots (e.g. `tank`) cannot be deleted via this endpoint — use `zpool destroy`.

#### Reference guard (409)

Destroy, rename (`POST /api/v1/datasets/{name}/rename`), and a `mountpoint` change (`PATCH /api/v1/datasets/{name}`) are refused with `409 Conflict` while dumpstore-managed configuration still refers to an affected dataset — for a recursive destroy or a rename, that includes every descendant. Nothing is changed. Checked references:

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

### GET /api/v1/snapshots

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

### POST /api/v1/snapshots

```json
{
  "dataset": "tank/data",
  "snapname": "2024-01-15_backup",
  "recursive": false
}
```

### POST /api/v1/snapshots/{snap}/clone

Clones a snapshot into a new writable dataset. Runs in-process as `zfs clone <snapshot> <target>`. The snapshot is the (percent-encoded) path parameter, e.g. `POST /api/v1/snapshots/tank%2Fdata%402026-05-06/clone`.

```json
{ "target": "tank/data-restore" }
```

Validation (`400`):
- snapshot and target are required
- `snapshot` must be `dataset@label` with a valid dataset name and label
- `target` must be a valid ZFS name that contains `/`

The snapshot is not checked for existence beforehand. If it is missing, or `target` already exists or is in a different pool, `zfs clone` fails and the request returns `500` with `tasks`.

On success, returns `201` and publishes `dataset.query` and `snapshot.query`.

```json
{ "snapshot": "tank/data@2026-05-06", "target": "tank/data-restore", "tasks": [ { "name": "Clone tank/data@2026-05-06 to tank/data-restore", "status": "changed" } ] }
```

### POST /api/v1/snapshots/{snap}/send

Dispatch `zfs send | zfs recv` as a background job. The snapshot is the (percent-encoded) path parameter. Returns `202 Accepted` immediately with the job metadata; live status is published on the `jobs.update` SSE topic and visible in the Jobs tab. Local target:

```json
{
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

Use `GET /api/v1/jobs/{id}` to poll status, or subscribe to the `jobs.update` SSE topic.

### GET /api/v1/snapshots/{snap}/diff

Show files changed between two snapshots, or between a snapshot and the live dataset, via `zfs diff -H`.

The older snapshot is the (percent-encoded) path parameter, e.g. `GET /api/v1/snapshots/tank%2Fdata%40before/diff?to=tank%2Fdata%40after`.

Query parameters:
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

### DELETE /api/v1/snapshots/{snap}@{snapname}

Destroys one snapshot. Runs in-process as `zfs destroy [-r] <dataset>@<snapname>`.

- `?recursive=true` passes `-r`. This also destroys the snapshot with the **same name in every descendant dataset**. It does not destroy clones: dependent clones make the destroy fail.
- Returns `400` if the path is not a valid `dataset@label`.
- Returns `404` if the snapshot does not exist.
- Does **not** release `dumpstore-repl` holds. A held replication snapshot fails with "dataset is busy". Use `POST /api/v1/snapshots/batch-delete`, which releases those holds first.

```json
{ "snapshot": "tank/data@2026-05-06", "tasks": [ { "name": "Destroy snapshot tank/data@2026-05-06", "status": "changed" } ] }
```

### POST /api/v1/snapshots/batch-delete

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

### GET /api/v1/operations/{id}

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

### GET /api/v1/jobs

Returns the list of known jobs, newest first.

### GET /api/v1/jobs/{id}

Returns a single job's full record (including stdout/stderr tail).

### POST /api/v1/jobs/{id}/cancel

Sends SIGTERM to the job's process group, escalating to SIGKILL after a 10 s grace. Returns `204 No Content` on success, `400` if the job is already terminal.

### DELETE /api/v1/jobs/{id}

Removes a terminal job from the manager and deletes its on-disk record. Returns `204 No Content` on success, `400` if the job is still running (cancel it first).

---

## Scheduled replication

Cron-scheduled replication tasks. Each fire creates a `dumpstore-repl-<UTC>` snapshot on the source, holds it for the duration of the transfer, picks the most recent common `dumpstore-repl-*` snapshot for an incremental send, dispatches the pipeline through the jobs runner, releases the hold, and prunes destination replication snapshots beyond the retention count.

### GET /api/v1/replication/tasks

Returns all replication tasks (without `last_runs`). Use `/history` to retrieve run records.

### POST /api/v1/replication/tasks

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

### PATCH /api/v1/replication/tasks/{id}

Partial update; any subset of the above fields is accepted. Re-registers the task with the scheduler.

### DELETE /api/v1/replication/tasks/{id}

Unregister and remove the task. Replicated snapshots on the destination, and `dumpstore-repl-*` snapshots on the source, are **not** deleted.

### POST /api/v1/replication/tasks/{id}/run

Fire the task immediately, off-schedule. Returns `202 Accepted` with the job_id of the dispatched send/recv pipeline. The body of the running task continues asynchronously; subscribe to `jobs.update` for completion.

### GET /api/v1/replication/tasks/{id}/history

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

### POST /api/v1/pools

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

### GET /api/v1/pools/importable

Scans with `zpool import` (no pool name) and returns pools available for import.

```json
[
  { "name": "tank", "id": "11536261005894085344", "state": "ONLINE" },
  { "name": "backup", "id": "9183745610293847561", "state": "DEGRADED", "status": "One or more devices are missing from the system." }
]
```

### POST /api/v1/pools/import

```json
{ "pool": "tank", "force": false }
```

`force: true` passes `-f` — needed for pools that were not cleanly exported or belonged to another system. Returns task steps (runs in-process, no playbook).

### POST /api/v1/pools/{pool}/export

Export the pool via `zpool export`. Fails when the pool is busy (open files, active shares, running jobs) — the error is surfaced in the op-log. Returns task steps (runs in-process, no playbook).

### POST /api/v1/pools/{pool}/vdevs

Add a data vdev via `zpool add`. **Irreversible on most pool layouts** — the UI gates this behind confirm-by-typing.

```json
{ "vdev_type": "mirror", "devices": ["/dev/sdd", "/dev/sde"] }
```

`vdev_type`: `single`, `mirror`, `raidz1`, `raidz2`, `raidz3` (minimum device counts enforced). Returns task steps (runs in-process, no playbook).

### POST /api/v1/pools/{pool}/cache · /log · /spare

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

### DELETE /api/v1/pools/{pool}/devices/{device}

Remove a cache, log, or spare device via `zpool remove`. zpool itself rejects removals that aren't legal for the given vdev. Returns task steps (runs in-process, no playbook).

---

## Devices & drive replacement

### GET /api/v1/devices

Returns the physical block devices on the host (Linux: `/sys/block`, skipping loop/ram/zvol/dm pseudo-devices; FreeBSD: `geom disk list`). `in_use_by` carries the pool name when the device currently backs a vdev (best-effort matching: by-id/by-path symlinks resolved, partition suffixes stripped).

```json
[
  { "name": "sdb", "path": "/dev/sdb", "size_bytes": 4000787030016, "model": "WDC WD40EFRX", "in_use_by": "tank" },
  { "name": "sdc", "path": "/dev/sdc", "size_bytes": 4000787030016, "model": "WDC WD40EFRX", "in_use_by": "" }
]
```

### POST /api/v1/pools/{pool}/devices/{device}/replace

Replace a vdev with a new device via `zpool replace`. A resilver onto the new device starts automatically; progress is visible in the pool's `scan` field (`GET /api/v1/pools/status`, `poolstatus` SSE topic).

```json
{ "new_device": "/dev/sdc" }
```

The `{device}` path parameter is the device being replaced: a device name, a path (percent-encoded, e.g. `%2Fdev%2Fdisk%2Fby-id%2F…`), or the numeric guid `zpool status` prints for missing devices. Returns task steps (runs in-process, no playbook).

### POST /api/v1/pools/{pool}/devices/{device}/offline

Take the `{device}` offline for maintenance via `zpool offline`. No body. Returns task steps (runs in-process, no playbook).

### POST /api/v1/pools/{pool}/devices/{device}/online

Bring the offlined `{device}` back via `zpool online`. No body. Returns task steps (runs in-process, no playbook).

---

## Pool scrub

### POST /api/v1/pools/{pool}/scrub

Start a scrub on the named pool. Returns task steps (runs in-process, no playbook).

### DELETE /api/v1/pools/{pool}/scrub

Cancel a running scrub on the named pool. Returns task steps (runs in-process, no playbook).

### GET /api/v1/pools/scrub-schedules

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

### PUT /api/v1/pools/{pool}/scrub-schedule

Add a pool to the periodic scrub schedule. On FreeBSD, an optional `threshold_days` body field sets how many days must elapse before a scrub is triggered.

```json
{ "threshold_days": 35 }
```

Returns Ansible task steps.

### DELETE /api/v1/pools/{pool}/scrub-schedule

Remove a pool from the periodic scrub schedule. Returns Ansible task steps.

---

## Auto-snapshot scheduling

Manages `com.sun:auto-snapshot*` ZFS user properties per dataset. Snapshots are created and pruned either by **dumpstore's built-in scheduler** (registered at startup when no OS daemon is active, or via `POST /api/v1/auto-snapshot/takeover`) or by the OS daemon — `zfs-auto-snapshot` (Linux) or `zfstools` (FreeBSD). Both use the same properties and the `zfs-auto-snap_<bucket>-YYYY-MM-DD-HHMM` naming, so either can prune the other's snapshots. `GET /api/v1/auto-snapshot/status` shows which one is active.

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

### GET /api/v1/auto-snapshot/schedules

Returns the `com.sun:auto-snapshot*` properties of every filesystem and volume, keyed by dataset name. Each property has the same `{value, source}` shape as `GET /api/v1/datasets/{name}/auto-snapshot`. Inherited values are already resolved by `zfs get`. An unset property returns `"-"` for both value and source.

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

The same payload is pushed on the `autosnapshot.query` SSE topic after every `PUT /api/v1/datasets/{name}/auto-snapshot`.

### GET /api/v1/auto-snapshot/status

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

### POST /api/v1/auto-snapshot/takeover

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

### POST /api/v1/auto-snapshot/release

The inverse of takeover. It first unregisters dumpstore's bucket tasks, then runs `auto_snapshot_release.yml`. The playbook:

- **Linux, cron packaging:** renames each `<file>.dumpstore-disabled` back to `<file>`, unless the package has recreated the file in the meantime.
- **Linux, systemd timers:** re-enables and starts the timers listed in `timer_state_file`. If there is no record, it enables every installed `zfs-auto-snapshot-*.timer`. It then deletes the record.
- **FreeBSD:** sets `daily_zfs_snapshot_enable="YES"` in `/etc/periodic.conf`.

The package is never reinstalled. Publishes `autosnap.status`.

```json
{ "tasks": [ { "name": "Linux — enable and start zfs-auto-snapshot systemd timers", "status": "changed" } ] }
```

Returns `500` if the autosnap runner is not initialised or the playbook fails. The bucket tasks are already unregistered at that point.

### GET /api/v1/datasets/{name}/auto-snapshot

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

### PUT /api/v1/datasets/{name}/auto-snapshot

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

On FreeBSD dumpstore owns `/etc/ctl.conf` completely (#186): every write re-reads the file, changes the target list, and renders the whole file again — one portal group per listen address, discovery allowed and filtered to what each initiator may reach (`discovery-filter portal-name`), and per-target auth. Writes are gated: `POST`/`DELETE /api/v1/iscsi/targets/{iqn}` return `409 "ctld not initialised — run POST /api/v1/iscsi/init first"` until init has run. After reloading ctld the playbook checks `ctladm portlist` and ctld's listening sockets; if the new config did not take effect (e.g. a portal IP not assigned to the host), the previous file is restored, ctld reloaded, and the request fails with the reason.

### GET /api/v1/iscsi/status

```json
{ "backend": "freebsd", "initialized": false, "conf_path": "/etc/ctl.conf", "conf_exists": true }
```

`backend` is `linux`, `freebsd`, or `""`. On Linux `initialized` is always `true` (targetcli has no init gate) and `conf_path` is empty.

### POST /api/v1/iscsi/init

FreeBSD only (`400` on Linux, `501` without ctld). Backs up an existing `/etc/ctl.conf` once to `/etc/ctl.conf.pre-dumpstore` (never overwritten), imports the targets dumpstore can represent — zvol-backed, LUN 0, one portal, auth none or a single CHAP credential, which includes files written by earlier dumpstore versions — and renders the file from template, then enables and reloads (or starts) ctld. Everything else (other LUN types, multi-portal targets, auth groups, `chap-mutual`, global settings) is not carried over; each item is reported as a `skipped` step `Not imported: …`. Safe to call again.

```json
{ "initialized": true, "imported": 2, "skipped": 1, "tasks": [ … ] }
```

### GET /api/v1/iscsi/targets

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

### POST /api/v1/iscsi/targets

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

Returns Ansible task steps. FreeBSD: `409` before init, if the IQN already exists, or if the zvol is already exported; `400` if `/dev/zvol/<zvol>` does not exist. CHAP credentials are never returned by `GET /api/v1/iscsi/targets`.

### DELETE /api/v1/iscsi/targets/{iqn}

Remove an iSCSI target and its backstore. The zvol is looked up from the target. Returns `{iqn, zvol, tasks}`; `404` if no target has that IQN. FreeBSD: `409` before init.

---

## Services

Manage the sharing daemons dumpstore controls (Samba, NFS, iSCSI). Status reads happen directly via `systemctl`/`service` — no Ansible overhead. Mutations (start/stop/restart/enable/disable) go through playbooks with op-log.

Logical service names: `samba`, `nfs`, `iscsi`, `wsdd`.

### GET /api/v1/services

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

### POST /api/v1/services/{service}/{action}

Control a service. Valid actions: `start`, `stop`, `restart`, `enable`, `disable`.

Returns Ansible task steps. `409` if the service is not installed on this host.

---

## Users & Groups

Local Unix users and groups. Reads parse `/etc/passwd` and `/etc/group` directly. Writes go through playbooks, are serialised server-side, and push fresh `user.query` / `group.query` SSE events when they succeed.

**System-account protection:** modify, delete and SSH-key writes return `403` when the account's UID/GID is below `UID_MIN` from `/etc/login.defs` (default `1000`). Modify and delete also return `403` for `nobody` and `nfsnobody` (users) and for `nogroup`, `nobody` and `nfsnobody` (groups), whatever their ID. The playbooks repeat the check with a fixed `>= 1000`.

Names (user, group, members, supplementary groups) must match `^[a-zA-Z_][a-zA-Z0-9._-]{0,31}$`. Lists are comma-separated. Paths (`shell`, `home`) must match `^/[a-zA-Z0-9/_.-]+$`. Passwords are **plaintext** — the OS hashes them (`chpasswd` on Linux, `pw usermod -h 0` on FreeBSD) — and must not contain newline characters.

A playbook failure, including a failed playbook `assert`, returns `500` with `tasks`.

### GET /api/v1/users

All entries in `/etc/passwd`, including system accounts.

```json
[
  { "username": "root",  "uid": 0,    "gid": 0,    "home": "/root",       "shell": "/bin/bash" },
  { "username": "alice", "uid": 1001, "gid": 1001, "home": "/home/alice", "shell": "/bin/bash" }
]
```

### POST /api/v1/users

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

### PUT /api/v1/users/{name}

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

### DELETE /api/v1/users/{name}

Delete a user **and their home directory** (`userdel` with remove and force). `404` if the user doesn't exist, `403` for system and protected accounts. Doesn't remove the user from Samba — use [`DELETE /api/v1/smb/users/{name}`](#delete-apismb-usersname).

```json
{ "username": "alice", "tasks": [ … ] }
```

### GET /api/v1/users/{name}/ssh-keys

Reads `<home>/.ssh/authorized_keys` and returns one entry per non-blank, non-`#` line, with its OpenSSH SHA256 fingerprint (identical to `ssh-keygen -lf`). A leading options field (`from="…",no-pty …`) is skipped when parsing. Lines that can't be parsed have empty `type`/`fingerprint` and can't be removed through the API. If the file is missing, `keys` is `[]`. `404` if the user doesn't exist.

```json
{ "keys": [ { "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… alice@laptop", "type": "ssh-ed25519", "fingerprint": "SHA256:a1ZSPmADOkmNRQozRPfkpT+3L/FxfP+/OF+7UHWjENQ", "comment": "alice@laptop" } ] }
```

### POST /api/v1/users/{name}/ssh-keys

Add an authorized key (`user_ssh_key_add.yml`). Creates `~/.ssh` (`0700`) and `authorized_keys` (`0600`) if they are missing, owned by `<user>:<user>`. The key is appended only if that exact line isn't already present.

```json
{ "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… alice@laptop" }
```

`key` is trimmed and must be a single line starting with `ssh-`, `ecdsa-sha2-` or `sk-` (`400` otherwise). `404` if the user doesn't exist, `403` if UID < `UID_MIN`.

```json
{ "tasks": [ … ] }
```

### DELETE /api/v1/users/{name}/ssh-keys/{fingerprint}

Remove the authorized key with that fingerprint (`user_ssh_key_remove.yml`). The fingerprint is the one returned by the list; percent-encode it (it can contain `/` and `+`), e.g. `DELETE /api/v1/users/alice/ssh-keys/SHA256%3Aa1ZSPmADOkmNRQozRPfkpT%2B3L%2FFxfP%2B%2FOF%2B7UHWjENQ`. No body.

`400` if the fingerprint doesn't start with `SHA256:`, `404` if the user or the key doesn't exist, `403` if UID < `UID_MIN`.

```json
{ "tasks": [ … ] }
```

### GET /api/v1/groups

All entries in `/etc/group`. `members` lists only supplementary members, never users whose primary group this is. It is always an array.

```json
[
  { "name": "wheel",   "gid": 0,    "members": [ "root" ] },
  { "name": "storage", "gid": 1500, "members": [ "alice", "bob" ] }
]
```

### POST /api/v1/groups

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

### PUT /api/v1/groups/{name}

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

### DELETE /api/v1/groups/{name}

Delete a local group (`group_delete.yml`). `404` if the group doesn't exist, `403` for system and protected groups.

```json
{ "groupname": "storage", "tasks": [ … ] }
```

---

## Dataset ownership

Owner and group of a dataset's **mountpoint directory**. Volumes, and datasets with `mountpoint=none`, return `400 "dataset … has no mountpoint"`.

### GET /api/v1/datasets/{name}/owner

```json
{ "dataset": "tank/data", "mountpoint": "/tank/data", "owner": "alice", "group": "storage" }
```

`owner` and `group` are numeric strings (e.g. `"1005"`) when the UID or GID has no name.

### PUT /api/v1/datasets/{name}/owner

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

### GET /api/v1/datasets/acl-status

Map of dataset name → `true`/`false` for every mounted filesystem (volumes and datasets with mountpoint `none`/`-` are left out). Used for the ACL badge in the datasets table.

```json
{ "tank": false, "tank/data": true, "tank/media": false }
```

A dataset is `true` if `getfacl` shows more than the three base entries (or `nfs4_getfacl` shows any entry), **or** if its `acltype` is anything other than `off`. So `true` means "extended entries present or ACLs enabled", not strictly "has extra entries". When neither tool is installed, the result is based on `acltype` alone. `500` only if `zfs list` fails.

### GET /api/v1/datasets/{name}/acl

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

### POST /api/v1/datasets/{name}/acl/entries

Add or modify an ACL entry. The `ace` string format depends on the dataset's `acltype`:

- **POSIX**: `setfacl -m` spec — `"user:alice:rwx"`, `"group:storage:r-x"`, `"default:user:alice:rwx"`
- **NFSv4**: full ACE string — `"A::alice@localdomain:rwaDxtTnNcCoy"`

```json
{ "ace": "user:alice:rwx", "recursive": false }
```

`recursive` (POSIX only) applies `setfacl -R` to all files inside the mountpoint.

### DELETE /api/v1/datasets/{name}/acl/entries?entry=\<spec\>

Remove an ACL entry. The `entry` query parameter:

- **POSIX**: `user:alice`, `default:group:storage`
- **NFSv4**: full ACE string to match

Append `&recursive=true` (POSIX only) to remove recursively.

---

## SMB shares & users

dumpstore owns `smb.conf` (`/etc/samba/smb.conf` on Linux, `/usr/local/etc/smb4.conf` on FreeBSD) and renders it from a template on every config write. Per-dataset shares are Samba **usershares** (`net usershare`), stored in dumpstore's own directory (`/etc/dumpstore/smb/usershares`, or `/usr/local/etc/dumpstore/smb/usershares` on FreeBSD).

**Init gate:** `GET /api/v1/smb/shares`, `PUT`/`DELETE /api/v1/smb/shares/{name}`, `POST`/`DELETE /api/v1/smb/users/{name}` (as well as homes, Time Machine and wsdd writes) return `409 "Samba not initialised — run POST /api/v1/smb/init first"` while `smb.conf` doesn't exist.

### GET /api/v1/smb/status

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

### POST /api/v1/smb/init

Initialise Samba for dumpstore. Safe to call again.

1. Returns `422 "Samba not installed — run: <install command>"` if `smbd` is missing.
2. `smb_init.yml`: copies an existing `smb.conf` to `smb.conf.pre-dumpstore` once (never overwritten). On Linux it ensures the `sambashare` group exists. It creates the usershares directory (`1770 root:sambashare` on Linux, `1755 root:wheel` on FreeBSD).
3. `smb_apply.yml`: renders `smb.conf` from the template and carries over `workgroup` and `server string` from the existing file (defaults `WORKGROUP` / `Samba Server`). It creates the referenced directories, deploys the file (keeping a backup), reloads Samba (`smbd`+`nmbd`, or `samba_server` on FreeBSD), and renders the wsdd options file if wsdd is installed.

```json
{ "initialized": true, "tasks": [ … ] }
```

`tasks` holds the steps from both playbooks, in order.

### GET /api/v1/smb/shares

Reads the usershare files directly, so the list also includes shares whose path no longer exists (`path_missing: true`). Samba hides those shares but re-activates them when the path reappears. Sorted by name; `[]` if there are none.

```json
[
  { "name": "data",  "path": "/tank/data",  "path_missing": false },
  { "name": "olds",  "path": "/tank/olds",  "path_missing": true }
]
```

### PUT /api/v1/smb/shares/{name}

Create or update a usershare for the dataset's mountpoint (`smb_usershare_set.yml` → `net usershare add <sharename> <mountpoint> "" everyone:F guest_ok=n`). Adding the share holds the [reference guard](#reference-guard-409) lock, so the dataset can't be destroyed or moved at the same moment. Once the share exists, it blocks destroy, rename and mountpoint changes of the dataset.

The share name is the path parameter (1–80 chars of `[a-zA-Z0-9._-]`); the body names the dataset:

```json
{ "dataset": "tank/data" }
```

`400` for an invalid dataset name or sharename. A dataset whose mountpoint is `none` or `-` fails the playbook (`500` with `tasks`).

```json
{ "dataset": "tank/data", "sharename": "data", "tasks": [ … ] }
```

### DELETE /api/v1/smb/shares/{name}

Remove a usershare (`smb_usershare_unset.yml` → `net usershare delete <sharename>`). Works for stale shares whose path no longer exists too. `404` if no usershare has that name. The response reports the path the share pointed to:

```json
{ "sharename": "data", "path": "/tank/data", "tasks": [ … ] }
```

### GET /api/v1/smb/users

Users in Samba's passdb (`pdbedit -L`). Not behind the init gate. If `pdbedit` isn't installed, the response is `200` with `available: false`.

```json
{ "available": true, "users": [ "alice", "bob" ] }
```

### PUT /api/v1/smb/users/{name}

Register an existing local user with Samba, or reset their Samba password (`smb_user_add.yml` → `smbpasswd -a -s`).

```json
{ "password": "s3cret" }
```

`password` is required and must not contain newline characters (`400`). The playbook fails (`500` with `tasks`) if `smbpasswd` is missing or the Unix user doesn't exist.

```json
{ "username": "alice", "tasks": [ … ] }
```

### DELETE /api/v1/smb/users/{name}

Remove a user from Samba's passdb (`smb_user_remove.yml` → `pdbedit -x -u`). The Unix account isn't touched.

```json
{ "username": "alice", "tasks": [ … ] }
```

---

## SMB Home Shares

Manage the Samba `[homes]` section in `smb.conf`. When enabled, each authenticated user automatically gets a personal share mapped to a subdirectory under the configured base path.

### GET /api/v1/smb/homes

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

### PUT /api/v1/smb/homes

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

### DELETE /api/v1/smb/homes

Remove the `[homes]` section from `smb.conf`. Returns Ansible task steps.

---

## Windows Discovery (wsdd)

wsdd announces the host in the Windows Network view. It advertises only the host and its workgroup, so dumpstore derives everything from `smb.conf` — there are no separate settings. `GET /api/v1/smb/status` carries a `wsdd` object:

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

### POST /api/v1/smb/wsdd/apply

Re-applies the current Samba config so the wsdd options file is rendered — for hosts where wsdd was installed after Samba was initialised. Returns Ansible task steps. `409` if Samba is not initialised, or wsdd is not installed / its unit does not read `$WSDD_PARAMS`.

---

## Time Machine Shares

Manage Samba shares configured as macOS Time Machine backup targets using `vfs_fruit` with catia and streams_xattr VFS modules.

### GET /api/v1/smb/timemachine

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

### PUT /api/v1/smb/timemachine/{name}

Create or update the Time Machine share named by the path parameter. Returns Ansible task steps.

```json
{
  "dataset": "tank/backups",
  "path": "/tank/backups/timemachine",
  "max_size": "1T",
  "valid_users": "alice bob"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `dataset` | no | ZFS dataset (alternative to specifying `path` directly) |
| `path` | yes | Filesystem path for the share |
| `max_size` | no | Maximum size quota for Time Machine backups |
| `valid_users` | no | Space-separated list of allowed users |

### DELETE /api/v1/smb/timemachine/{name}

Remove a Time Machine share from `smb.conf`. Returns Ansible task steps.

---

## TLS / HTTPS

TLS settings live in `dumpstore.conf` (`tls_enabled`, `tls_cert_path`, `tls_key_path`), written by `tls_set_config.yml`. **No endpoint switches the running server to HTTPS.** HTTPS starts only when dumpstore is restarted with `--tls` and both cert paths are set. The certificate is read once at startup, so a restart is also needed after every gencert/issue/renew. With `--tls`, `-addr` is ignored: HTTPS listens on `--tls-port` (default `443`) and plain HTTP on `--http-port` (default `80`) answers with `301` to `https://`.

Path fields must be absolute and must not contain NUL, `;`, `|`, `&`, `$` or `` ` ``. Hostnames and domains must be DNS names (labels of letters, digits and `-`).

### GET /api/v1/tls

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
| `acme_email`, `acme_domain` | Present only after `POST /api/v1/tls/acme/issue` in the current process (not persisted, see below) |

All fields except `enabled` are omitted when empty.

### POST /api/v1/tls/self-signed

Generate a self-signed certificate and point the config at it.

```json
{ "hostname": "nas.local", "cert_dir": "/etc/dumpstore/tls" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `hostname` | yes | Becomes CN and DNS SAN |
| `cert_dir` | no | Default `<config dir>/tls` (e.g. `/etc/dumpstore/tls`, `/usr/local/etc/dumpstore/tls`). Must be an absolute path of `A-Z a-z 0-9 . _ / -` only |

Runs `tls_gencert.yml`: creates `cert_dir` (`0700`), then `openssl req -x509` with an ECDSA P-256 key, valid 3650 days, SAN `IP:127.0.0.1,DNS:<hostname>`, written to `cert.pem` / `key.pem` (key `0600`). **If `cert.pem` already exists it is not regenerated**, even if `hostname` is different. Then `tls_set_config.yml` stores both paths and `tls_enabled: true`. Returns the Ansible task steps of both playbooks.

`400` missing/invalid `hostname` or invalid `cert_dir` · `500` playbook failure.

### PATCH /api/v1/tls

Use an existing certificate + key from disk (e.g. from certbot or acme.sh).

```json
{ "cert_path": "/etc/letsencrypt/live/nas.example.com/fullchain.pem",
  "key_path":  "/etc/letsencrypt/live/nas.example.com/privkey.pem" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `cert_path` | yes | PEM certificate (chain) |
| `key_path` | yes | PEM private key matching the certificate |

Both paths must be absolute and consist of `A-Z a-z 0-9 . _ / -` only (`400` otherwise); the same rule applies to `cert_dir` for self-signed and ACME certificates.

Before anything is written, the pair is loaded in Go (`tls.LoadX509KeyPair`), so files that are missing, unreadable or don't match are rejected. Then `tls_set_config.yml` stores the paths and `tls_enabled: true`. Files are not copied. Returns Ansible task steps.

`400` missing/invalid path or `invalid cert/key pair: …` · `500` playbook failure.

### POST /api/v1/tls/acme/issue

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

### POST /api/v1/tls/acme/renew

No request body. Runs `tls_acme_renew.yml` → `lego --email … --domains … --path <cert_dir> --tls renew --days 30` using the email/domain from the last issue. lego renews only when the certificate expires within 30 days; otherwise the call succeeds without changing anything. `cert_dir` is derived from the configured cert path (two levels up from `certificates/<domain>.crt`).

**Renewal is manual.** dumpstore has no renewal timer. Same port-443 constraint as issue, so this fails while dumpstore itself serves HTTPS on 443. Restart afterwards to load the renewed certificate. Returns Ansible task steps.

`400 "ACME is not configured"` if no issue has run since the process started (includes every restart after an issue) · `400 "ACME email and domain are not set"` · `500` lego missing or renewal failed.

---

## Server-Sent Events

### GET /api/v1/events

Subscribe to live data updates. The server pushes named events whenever data changes.

**Query parameter:** `topics` — comma-separated list of topics to subscribe to.

**Available topics:**

| Topic                | Data                                            | Cadence |
|----------------------|-------------------------------------------------|---------|
| `pool.query`         | Same JSON as `GET /api/v1/pools`                   | Every 10 s on change |
| `poolstatus`         | Same JSON as `GET /api/v1/pools/status`              | Every 10 s on change |
| `dataset.query`      | Same JSON as `GET /api/v1/datasets`                | Every 10 s on change |
| `snapshot.query`     | Same JSON as `GET /api/v1/snapshots`               | Every 10 s on change |
| `autosnapshot.query` | Same JSON as `GET /api/v1/auto-snapshot/schedules` | Every 10 s on change |
| `iostat`             | Same JSON as `GET /api/v1/pools/iostat`                  | Every 10 s |
| `user.query`         | Same JSON as `GET /api/v1/users`                   | Every 10 s on change + after writes |
| `group.query`        | Same JSON as `GET /api/v1/groups`                  | Every 10 s on change + after writes |
| `service.query`      | Same JSON as `GET /api/v1/services`                | Every 10 s on change |
| `jobs.update`        | Single job object (as `GET /api/v1/jobs/{id}`)     | On every job state change |
| `replication.update` | Same JSON as `GET /api/v1/replication/tasks`             | On task create/update/delete + after each scheduled run |
| `autosnap.status`    | Same JSON as `GET /api/v1/auto-snapshot/status`    | After takeover / release |
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
curl -N 'http://localhost:8080/api/v1/events?topics=pool.query,iostat'
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
