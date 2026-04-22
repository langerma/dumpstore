# Changelog

All notable changes to this project will be documented here.

## [Unreleased]

### Added
- **Dev VM environment** — `make vm-linux-start/deploy` and `make vm-freebsd-start/deploy` spin up headless Lima VMs (Ubuntu 24.04 + FreeBSD 15) with ZFS, Ansible, and Go pre-installed; source is packed and `make install` runs natively inside the VM; Linux UI at http://localhost:8080, FreeBSD at http://localhost:8081; VMs use dedicated extra disks for ZFS (`dumpstore-linux-data`, `dumpstore-freebsd-data`); default credentials admin/admin; closes #83

### Changed
- **authCfg thread safety** — added `sync.RWMutex` to protect concurrent reads/writes of in-memory auth config (`PasswordHash`, `Username`, TLS/ACME fields); eliminates a data race under concurrent requests
- **Consistent JSON decoding** — all API endpoints now use the `decodeJSON` helper (rejects trailing garbage after the JSON value); 8 endpoints previously used raw `json.NewDecoder().Decode()`
- **Handler boilerplate reduction** — new `writeRunOpError` helper replaces 33 instances of the 5-line error-handling pattern after `runOp` calls; ~165 lines removed
- **Consistent SSE publish after mutations** — `createDataset`, `deleteDataset`, `createSnapshot`, `deleteSnapshot`, `deleteSnapshotBatch`, `startScrub`, and `cancelScrub` now immediately push SSE updates (previously relied on the 10 s poller); new `publishSnapshots()` and `publishPools()` helpers
- **Logging extracted from main.go** — `journalHandler` and `requestLogger` middleware moved to new `internal/logging/` package (`NewJournalHandler`, `RequestLogger`); `main.go` reduced from 345 to 180 lines
- **Makefile** — BSD/GNU make compatible (no `$(shell ...)` at parse time); `check-prereqs` now auto-installs Go (from go.dev) and Ansible (`apt-get`/`pkg`) if absent; `install.sh` reduced to a thin root-check wrapper around `make install`
- **FreeBSD VM port forwarding** — SSH tunnel (`-L 8081:127.0.0.1:8080`) set up in `vm-freebsd-start` as Lima's guest agent is Linux/Darwin-only; torn down in `vm-freebsd-stop`

### Fixed
- **SMB apply with no dirs** — `DirsToCreate()` returned Go `nil` slice, marshalled to JSON `null`, which caused Ansible's `from_json` to return Python `None` and fail the loop; fixed by initialising to `[]string{}`; added `| default([])` guard in `smb_apply.yml`

---

## [v0.1.10] — 2026-04-14

### Added
- **TLS / HTTPS support** — `--tls` flag enables HTTPS; self-signed ECDSA-P256 cert generation via `tls_gencert.yml` (openssl); path loader (`PATCH /api/tls/config`) validates and loads existing certs (Let's Encrypt, Certbot, acme.sh); ACME issuance and renewal via `lego` (`POST /api/tls/acme/issue`, `POST /api/tls/acme/renew`); HTTP→HTTPS redirect listener on `--http-port` (default 80); TLS status card in Users tab with cert CN, SANs, expiry countdown; `lego` is an optional dependency (warn if missing and ACME is configured)
- **Service management** — new Services tab with start/stop/restart/enable/disable controls for Samba, NFS, and iSCSI; `GET /api/services` returns live status for all three; mutations go through `service_control_linux.yml` (systemd) or `service_control_freebsd.yml` (rc.d) with full op-log display; status updates via SSE every 10 s; NFS stop shows a client-disconnect warning
- **Network interface overview** — `GET /api/network` returns all interfaces with name, state (up/down), MAC, MTU, IPv4/IPv6 addresses, link speed, and RX/TX byte counters; displayed as a Network section in the Pools tab with state badges and muted virtual/loopback rows; Linux reads speed and counters from `/sys/class/net`; FreeBSD parses a single `ifconfig -a` call for speed
- **SMB init status badge** — Users & Groups tab shows a green "Initialised" badge with last-applied timestamp (from `conf_mtime` in `GET /api/smb/status`), or red "Not initialised" when `smb.conf` is absent; updates live after `POST /api/smb/init`

### Changed
- **Samba full ownership model** — dumpstore now owns `smb.conf` / `smb4.conf` entirely. All Samba config changes (homes, Time Machine shares) render the complete config from a Go template and deploy it atomically via the new `smb_apply.yml` playbook; block-patching playbooks (`smb_setup.yml`, `smb_homes_set.yml`, `smb_homes_unset.yml`, `smb_timemachine_set.yml`, `smb_timemachine_unset.yml`) are removed. Referenced directories (homes base path, TM target paths) are created automatically as part of apply.
- **Samba initialisation gate** — all Samba sub-features (home shares, Time Machine, SMB users, usershares) are now disabled in the UI and return HTTP 409 from the API until `POST /api/smb/init` has been called. The button is renamed "Initialize Samba".
- **FreeBSD smb4.conf bootstrap** — `smb_init.yml` creates a minimal `/usr/local/etc/smb4.conf` on FreeBSD if none exists (Linux packages provide one). Resolves #77.
- **FreeBSD-compliant config paths** — dumpstore now uses `/usr/local/etc/dumpstore/` on FreeBSD (instead of `/etc/dumpstore/`) for its config file, TLS certificates, and Samba usershares directory. A new `internal/platform` package provides `ConfigDir(goos)` as the single source of truth. `Makefile`, `install.sh`, and `contrib/dumpstore.rc` updated accordingly.
- **New endpoints**: `GET /api/smb/status` returns `{initialized, conf_path, os, conf_mtime}`; `POST /api/smb/init` replaces `POST /api/smb-config/pam`.
- **New Go package** `internal/smb` — owns `SMBConfig`, parsing, template rendering, and OS helpers.

### Fixed
- **FreeBSD rc script** — rewrote `contrib/dumpstore.rc` to follow the standard `daemon(8)` pattern (`-p` child pidfile + `procname`); fixes start/restart failures caused by stale supervisor processes, missing PATH for `ansible-playbook`, and silent crashes (output now goes to `/var/log/dumpstore.log` and syslog via `-S -T dumpstore`)
- **FreeBSD build** — added `-buildvcs=false` to `go build` in both `Makefile` and `install.sh` to fix VCS stamping failure in jails and on certain mount types

---

## [v0.1.9] — 2026-04-03

### Added
- **Authentication** — session-based login with bcrypt-hashed password stored in `/etc/dumpstore/dumpstore.conf`; `--set-password` CLI subcommand; per-IP login rate limiting (10 attempts/60 s); reverse proxy delegation via `X-Remote-User` from configured trusted CIDRs; login page matches dark monospace theme; logout button and username badge in header; `/metrics` excluded from auth by default; no-password startup binds to loopback only with a warning
- **Auth settings UI** — Change Password and Change Username dialogs in Users & Groups tab; both go through Ansible playbooks and show the operation log
- **Audit logging** — all mutating API operations (dataset, snapshot, user, group, ACL, SMB, iSCSI) now emit a structured `slog` audit record with operation, target, actor IP, and outcome
- **Client-side name validation** — dataset and snapshot create dialogs validate names against `reZFSName` / `reSnapLabel` before submitting; inline error shown immediately instead of a round-trip
- **SSE status badge** — header badge shows "live" (SSE connected) vs "polling" (30 s REST fallback) so users know when they are seeing stale data
- **Feature roadmap** — full planned feature backlog tracked in `FEATURES.md` with linked GitHub issues
- **Code of Conduct**, **Contributing guidelines**, and **GitHub issue templates** (bug report + feature request)

### Fixed
- `auditLog` was writing outcome to `args[5]` instead of `args[7]` — error cases logged `outcome=ok` and corrupted the target field
- `toast()` calls used `'error'` instead of `'err'`; only `.toast.err` exists in CSS so error toasts were unstyled
- `reZFSName` / `reSnapLabel` validation regexes duplicated across `datasets.js` and `snapshots.js`; consolidated into `utils.js`
- Numeric ZFS properties (`quota`, `recordsize`, `volsize`, etc.) now validated for upper-bound sanity before being sent to Ansible
- Dataset, snapshot, and ACL handlers now pre-check existence and return a clean 404 before running a playbook
- CHAP password validated with `safePassword` instead of the looser `safePropertyValue`
- Lagging SSE subscribers are now closed instead of silently dropping messages — frontend detects the disconnect and falls back to polling
- Critical scanner buffer exhaustion under high Ansible output — raised from 64 KB to 4 MB
- Nil slices in SSE payloads serialized as `null` instead of `[]`, causing silent frontend render failures

### Changed
- `app.js` split into per-tab ES modules — `datasets.js`, `snapshots.js`, `users.js`, `pools.js`, etc.; no logic changes
- `handlers.go` split into domain-specific files — `zfs_handlers.go`, `user_handlers.go`, `acl_handlers.go`, `smb_handlers.go`, `iscsi_handlers.go`; no logic changes

---

## [v0.1.8] — 2026-04-02

### Added
- **Request ID correlation** — per-request `req_id` UUID on all `slog` log lines; reads `X-Request-ID` from upstream proxies (nginx, Traefik) and echoes it back in the response header; enables full request lifecycle reconstruction from logs

---

## [v0.1.7] — 2026-04-01

### Added
- **SSH key management** — add and remove SSH authorized keys per user via `GET/POST/DELETE /api/users/{name}/ssh-keys`; keys validated against known key types before storage
- **Home directory migration** — edit user dialog accepts a new home path; Ansible's `user` module moves files atomically with `move_home: true`
- **Samba password sync** — editing a user's Unix password automatically updates their Samba tdbsam entry if they are a registered SMB user

### Fixed
- Passwords containing newline or carriage return characters are now rejected — previously a `\n` in a password corrupted `smbpasswd` stdin input

### Changed
- `handlers.go` refactored into domain-specific files (`zfs_handlers.go`, `user_handlers.go`, `acl_handlers.go`, `smb_handlers.go`, `iscsi_handlers.go`) for navigability; no logic changes

---

## [v0.1.6] — 2026-03-25

### Added
- **Time Machine shares** — create and remove Samba `vfs_fruit` Time Machine backup targets backed by ZFS datasets; configurable max size and valid users list; `GET/POST /api/smb/timemachine`, `DELETE /api/smb/timemachine/{sharename}`

---

## [v0.1.5] — 2026-03-25

### Added
- **SMB home shares** — enable and configure the Samba `[homes]` section from the UI; dataset picker or custom path; per-user auto-shares; `GET/POST/DELETE /api/smb/homes`

---

## [v0.1.4] — 2026-03-15

### Added
- **iSCSI target management** — expose zvols as iSCSI targets on Linux (`targetcli`/LIO) and FreeBSD (`ctld`); dialog with IQN, portal configuration, CHAP authentication, and initiator ACL management

---

## [v0.1.3] — 2026-03-15

### Added
- **Auto-snapshot scheduling** — manage `com.sun:auto-snapshot*` ZFS properties per dataset from the UI; integrates with `zfs-auto-snapshot` on Linux and `zfstools` on FreeBSD
- **Multi-snapshot delete** — checkbox selection for batch snapshot deletion

### Fixed
- Pools and datasets now render immediately on connect after server restart or host reboot instead of waiting for the first poll cycle

---

## [v0.1.2] — 2026-03-13

### Added
- **Pool scrub scheduling** — configure periodic scrub schedules per pool (Linux: `zfsutils-linux` monthly cron; FreeBSD: `periodic.conf` configurable threshold)
- **Schema-driven UI** — ZFS property allowed values and user shells defined once in `schema.go`, compiled into Ansible vars files at startup; eliminates duplication between frontend, backend, and playbooks
- CI workflow to enforce docs updates on every PR (`check-docs.yml`)

---

## [v0.1.1] — 2026-03-10

### Added
- **Pool scrub management** — trigger scrubs, cancel running scrubs, view last scrub time/status/progress per pool
- **Live Ansible task streaming** — task results streamed over SSE as they complete; op-log dialog updates in real time without waiting for the playbook to finish
- **GitHub Pages landing page** — project homepage at `langerma.github.io/dumpstore`
- Per-playbook timeout in the Ansible runner (default 5 minutes)
- System user/group toggle in Users & Groups tab — show/hide accounts below `UID_MIN`

### Security
- Input validation migrated from character denylist to whitelist regexes across all handlers

### Fixed
- Op-log overlay now appears immediately when a write operation starts, before the first Ansible task result arrives
- Active navigation tab highlighted correctly on click

---

## [v0.1.0] — 2026-03-07

### Added
- **SMB share management** — per-dataset Samba usershares via `net usershare add/delete`; SMB button on each filesystem dataset row opens a dialog showing the current share name, with Share and Remove actions; button highlights when sharing is active
- `GET /api/smb-shares` — lists all active usershares (name → path mapping)
- `POST /api/smb-share/{dataset}` / `DELETE /api/smb-share/{dataset}` — create and remove usershares backed by `smb_usershare_set.yml` / `smb_usershare_unset.yml`
- **Samba user management** — `GET/POST/DELETE /api/smb-users/{name}` registers and removes users from the tdbsam database (`smbpasswd -a` / `pdbedit -x`); Samba users panel in the UI lists registered users with add and remove actions
- **One-click Samba setup** — `POST /api/smb-config/pam` runs `smb_setup.yml` which configures the usershares directory, removes the `[homes]` section so home directories are not shared by default, and enables PAM passthrough on Linux; cross-platform: auto-detects Linux vs FreeBSD and sets the correct `smb.conf` path, usershares directory, and service names

### Fixed
- Samba setup no longer leaves `[homes]` enabled — home directories are explicitly removed from `smb.conf` so they are never shared by default
- `smb_setup.yml` is now cross-platform: Linux uses `/etc/samba/smb.conf` + `smbd`/`nmbd`; FreeBSD uses `/usr/local/etc/smb4.conf` + `samba_server`

## [v0.0.9] — 2026-03-06

### Added
- **NFS share management** — per-dataset NFS sharing via the ZFS `sharenfs` property; NFS button on each filesystem dataset row opens a dialog showing the current share options, with Share and Disable actions; button highlights with accent colour when sharing is active, tooltip shows the current options string
- `sharenfs` property readable via `GET /api/dataset-props/{name}` and writable via `PATCH /api/datasets/{name}`; backed by `zfs_dataset_set.yml`
- `publishDatasets()` — dataset list is pushed to all SSE subscribers immediately after any property change, so the NFS button state updates in real time across all open tabs
- `ShareNFS` field on the `Dataset` struct; `sharenfs` column included in `ListDatasets` so SSE carries NFS state without an extra round-trip
- **Installed Software** — "NFS server" row added to the Sysinfo tab; probes `exportfs` on Linux and `mountd` on FreeBSD
- **Requirements** — NFS server packages (`nfs-kernel-server` / `nfs-utils`) documented in README requirements table and install snippets

## [v0.0.8] — 2026-03-06

### Added
- **Enhanced Prometheus metrics** — `GET /metrics` now exposes HTTP request counters (`http_requests_total{method,path,status}`) and latency histograms (`http_request_duration_seconds{method,path}`), plus Ansible playbook counters (`ansible_runs_total{playbook,status}`) and duration histograms (`ansible_run_duration_seconds{playbook}`); paths are normalised to keep cardinality low; static file requests are excluded
- **Install script** — `install.sh` builds and installs the binary, playbooks, and static files, and registers the service on both Linux (systemd) and FreeBSD (rc.d); also supports `--uninstall`
- **BSD 2-Clause License**

## [v0.0.7] — 2026-03-05

### Added
- **Software inventory** — `/api/sysinfo` now probes and returns versions of all external tools used at runtime: ZFS, Ansible, Python, smartctl, nfs4-acl-tools, setfacl, and the system package manager; missing tools are reported as N/A
- **Installed Software section** — dedicated table on the Sysinfo tab, displayed directly below the Host info card
- **Dataset mountpoint ownership management** — `GET/POST /api/chown/{dataset}` shows and sets the owner/group of a dataset's mountpoint via Ansible; chown button added to the dataset row

### Changed
- Sysinfo tab layout overhauled: Host card now has a section header; Storage Pools and I/O Statistics rendered side-by-side in a 50/50 grid, each filling its half
- Dumpstore version moved from the ZFS version bar into the sticky header badge
- Pool device names wrap instead of truncating with ellipsis
- I/O Statistics table stretches to fill its column; redundant section label removed

### Fixed
- Duplicate `v` prefix in the header version badge (version string already contains the prefix from the build tag)
- Missing `btn-chown` CSS style that caused the chown button to be invisible
- `syslog` priority prefixes now emitted correctly so journald maps log levels to the right `PRIORITY` field

## [v0.0.6] — 2026-03-04

### Fixed
- **SSE initial state** — new subscribers now receive the current state immediately on connect instead of waiting for the next data-change poll cycle; the broker caches the last published payload per topic and delivers it synchronously in `Subscribe()`, under the same mutex as the subscriber-list update to prevent a race with concurrent `Publish()` calls
- **SSE connection stability** — a 30-second keepalive comment (`: keepalive`) is sent on idle streams so proxies and NAT devices do not drop connections to topics where data rarely changes (e.g. `user.query`, `group.query`)

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
