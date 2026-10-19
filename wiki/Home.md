# dumpstore

A lightweight NAS management UI written in Go — built for Linux and FreeBSD, designed to stay out of the way of a vanilla system.

No container runtime, no database, no Node.js. Just a single compiled binary, some Ansible playbooks, and a handful of static files.

dumpstore **manages** ZFS and sharing (SMB, NFS, iSCSI, wsdd, and the local users and permissions they need) and owns the config of every service it manages. Everything else — identity, monitoring, UPS, notifications — it only **integrates** with.

## Features

**Pools & disks**
- Pool overview with health, usage, fragmentation, dedup ratio, vdev tree, live I/O statistics, and S.M.A.R.T. health per drive
- Create, import, and export pools; add data vdevs, cache, log, and spare devices
- Replace, offline, and online devices with resilver progress
- Start/cancel scrubs and configure periodic scrub schedules

**Datasets & snapshots**
- Per-pool dataset browser with a detail drawer for inline property editing, sharing, permissions, and snapshots
- Create, rename, and destroy datasets and volumes; per-user/group space usage and quotas; `zfs rewrite` as a background job
- Snapshot create, clone, diff, and (batch) delete
- Native auto-snapshot scheduling for `com.sun:auto-snapshot*`, with takeover from / release back to the OS daemon

**Replication & jobs**
- One-shot `zfs send | zfs recv` to a local pool or over SSH, incremental and `--raw`
- Cron-scheduled replication with holds, incremental selection, and retention pruning
- Long-running work runs as cancellable background jobs that survive restarts

**Sharing**
- NFS via the ZFS `sharenfs` property
- SMB usershares, Samba users, `[homes]`, and Time Machine shares — dumpstore owns `smb.conf`
- Windows network discovery via wsdd, kept in sync with Samba's workgroup
- iSCSI targets for zvols (`targetcli`/LIO on Linux, `ctld` on FreeBSD) with CHAP and initiator ACLs
- Start, stop, enable, and disable Samba, NFS, iSCSI, and wsdd from the Services tab

**Users & permissions**
- Local users (including SSH keys and Samba password sync) and groups
- POSIX and NFSv4 ACLs (NFSv4 on FreeBSD), dataset owner/group

**Operations**
- Session login with argon2id, reverse-proxy delegation (`X-Remote-User`), TLS with self-signed, existing, or ACME certificates
- Live UI updates over Server-Sent Events
- Prometheus `/metrics` and optional OpenTelemetry export (traces, logs, metrics)
- Per-request `req_id` log correlation and an audit line for every mutating operation

The full list with versions, and what is planned, is in [FEATURES.md](https://github.com/langerma/dumpstore/blob/main/FEATURES.md).

## Quick start

```bash
git clone https://github.com/langerma/dumpstore.git
cd dumpstore
sudo ./install.sh
```

The installer prompts for the admin password, and the service listens on `http://<host>:8080`. See [[Installation]] for requirements, options, and configuration.

## Security

dumpstore has built-in session-based authentication (argon2id). If no password is configured the service binds to `127.0.0.1` only. It runs as root (required for ZFS) — see [SECURITY.md](https://github.com/langerma/dumpstore/blob/main/SECURITY.md) for TLS and the recommended deployment topology.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](https://github.com/langerma/dumpstore/blob/main/CONTRIBUTING.md) for the workflow, conventions, and docs update requirements. This project follows a [Code of Conduct](https://github.com/langerma/dumpstore/blob/main/CODE_OF_CONDUCT.md).

## Pages

- [[Installation]] — requirements, install options, authentication, service configuration, observability, versioning
- [[Architecture]] — execution lanes, service ownership, frontend, observability internals, project layout, testing
- [[API Reference]] — every endpoint with request/response examples, and the SSE event stream
- [[Screenshots]] — every tab and dialog
