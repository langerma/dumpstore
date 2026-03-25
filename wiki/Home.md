# dumpstore

A lightweight NAS management UI written in Go — built for Linux and FreeBSD, designed to stay out of the way of a vanilla system.

No container runtime, no database, no Node.js. Just a single compiled binary, some Ansible playbooks, and a handful of static files.

## Features

- **System info** — hostname, OS, kernel, CPU, uptime, load averages, process stats
- **Pool overview** — health badges, usage bars, fragmentation, deduplication ratio, vdev tree
- **Pool scrub management** — trigger scrubs, cancel running scrubs, view last scrub time/status/progress per pool; configure periodic scrub schedules (Linux: `zfsutils-linux`; FreeBSD: `periodic.conf`)
- **I/O statistics** — live read/write IOPS and bandwidth per pool
- **Disk health** — S.M.A.R.T. data per drive (temperature, power-on hours, reallocated sectors, pending sectors, uncorrectable errors)
- **Dataset browser** — depth-indented collapsible tree, compression, quota, mountpoint; ACL, NFS, and SMB buttons light up when configured
- **Dataset creation** — create filesystems and volumes with any combination of ZFS properties
- **Dataset editing** — update properties in place (set or inherit)
- **Dataset deletion** — destroy datasets and volumes with recursive option and confirm-by-typing dialog
- **Snapshot management** — list, create (recursive), and delete snapshots
- **User management** — list, create, edit, and delete local users; system users (uid < 1000) hidden by default
- **Group management** — list, create, edit, and delete local groups; system groups hidden by default
- **NFS share management** — enable, configure, and disable NFS sharing per dataset via the ZFS `sharenfs` property; cross-platform
- **SMB share management** — create and remove Samba usershares; manage Samba users; one-click Samba setup
- **SMB home shares** — enable/configure the Samba `[homes]` section for automatic per-user home directory shares; configurable base path, browseable, read only, create/directory masks
- **Time Machine shares** — create Samba shares configured as macOS Time Machine backup targets using `vfs_fruit`; multiple named shares each backed by a different ZFS dataset; configurable max size quota and valid users
- **iSCSI target management** — expose ZFS volumes as iSCSI targets via `targetcli`/LIO on Linux or `ctld` on FreeBSD; per-zvol dialog with IQN, portal IP/port, auth mode (None/CHAP), and initiator ACL list
- **ACL management** — POSIX ACL and NFSv4 ACL entries per dataset; recursive apply supported
- **Live updates** — Server-Sent Events push changes every 10 s; falls back to 30 s REST polling
- **Prometheus metrics** — Go runtime, HTTP request counters/latency, Ansible playbook metrics at `GET /metrics`

## Planned

- **User mgmt extensions** — SSH key management (`authorized_keys`), move home directory
- **ZFS native encryption** — load/unload keys, encryption status per dataset, keyformat/keylocation support
- **Pool import/export** — import available pools from attached devices; export pools safely
- **ZFS send/receive** — pool replication and off-site backup

## Quick start

```bash
git clone https://github.com/langerma/dumpstore.git
cd dumpstore
sudo ./install.sh
```

The service starts automatically and listens on `http://localhost:8080`.

See [[Installation]] for detailed instructions, requirements, and configuration.

## Security

dumpstore has no built-in authentication and runs as root. It is designed for trusted, private networks. Several endpoints accept passwords in the request body — without TLS these travel in plaintext. See [SECURITY.md](https://github.com/langerma/dumpstore/blob/main/SECURITY.md) for recommended mitigations (reverse proxy with TLS, SSH tunnel, VPN) and rate-limiting guidance.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](https://github.com/langerma/dumpstore/blob/main/CONTRIBUTING.md) for the workflow, conventions, and docs update requirements. This project follows a [Code of Conduct](https://github.com/langerma/dumpstore/blob/main/CODE_OF_CONDUCT.md).

## Pages

- [[Installation]] — requirements, install script, make, manual build, service configuration
- [[API Reference]] — full REST API documentation with request/response examples
- [[Architecture]] — design overview, read/write split, SSE stream, request flow
