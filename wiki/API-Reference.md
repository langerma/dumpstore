# API Reference

All endpoints are served at `http://<host>:8080`. The API is JSON-over-HTTP; all request and response bodies are `application/json`.

## Endpoint overview

| Method | Path | Description |
|--------|------|-------------|
| GET    | `/api/sysinfo`              | Host and process info |
| GET    | `/api/version`              | OpenZFS version string |
| GET    | `/api/pools`                | List all pools with usage stats |
| GET    | `/api/poolstatus`           | Detailed pool status with vdev tree |
| GET    | `/api/datasets`             | List all datasets and volumes |
| GET    | `/api/dataset-props/{name}` | Editable properties for a dataset |
| GET    | `/api/snapshots`            | List all snapshots |
| GET    | `/api/iostat`               | Pool I/O statistics (1-second sample) |
| GET    | `/api/smart`                | S.M.A.R.T. health per disk |
| GET    | `/api/events`               | Server-Sent Events stream |
| GET    | `/metrics`                  | Prometheus text exposition |
| POST   | `/api/datasets`             | Create a dataset or volume |
| PATCH  | `/api/datasets/{name}`      | Update dataset properties |
| DELETE | `/api/datasets/{name}`      | Destroy a dataset or volume |
| POST   | `/api/snapshots`            | Create a snapshot |
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
| POST   | `/api/scrub/{pool}`         | Start a pool scrub |
| DELETE | `/api/scrub/{pool}`         | Cancel a running pool scrub |

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

### DELETE /api/snapshots/{dataset}@{snapname}

Append `?recursive=true` to also destroy clones.

---

## Pool scrub

### POST /api/scrub/{pool}

Start a scrub on the named pool. Returns Ansible task steps.

### DELETE /api/scrub/{pool}

Cancel a running scrub on the named pool. Returns Ansible task steps.

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
