# Changelog

All notable changes to this project will be documented here.

## [Unreleased]

### Added

- **ZFS capability detection and UI gating** — dumpstore now probes what the installed OpenZFS release actually supports (`internal/zfs.Caps()`: `zfs rewrite` subcommand recognition, draid in the `zpool upgrade -v` feature list — probing rather than version comparison, since distros backport) and exposes the result as a `capabilities` object in `GET /api/schema`. The UI gates accordingly: the "Rewrite existing blocks" section in Edit Dataset is replaced by an explanatory note on hosts without `zfs rewrite` (needs OpenZFS ≥ 2.3), draid topology options in Create Pool are disabled with a tooltip when the draid feature is absent, and the Sysinfo tab gained a ZFS section showing the version plus capability badges. No behavior change on hosts that support everything. Closes #119.

- **Scope boundary: manage vs integrate** — the project's scope rule is now written down (CLAUDE.md "Scope boundary" section, README philosophy paragraph, wiki Architecture page): dumpstore *manages* ZFS (pools, datasets, snapshots, replication, encryption) and sharing (SMB/NFS/iSCSI/wsdd) plus the local users they need; it only *integrates* with identity (lldap), observability (Prometheus/OTEL), power (NUT), and notifications — read, display, link out, minimal in-tree surface. The FEATURES.md Planned table carries a per-row Scope verdict, and the open integrate-domain issues (#62 lldap, #52 NUT, #49 OTEL) were annotated and scope-trimmed accordingly. Closes #121.

- **Release build smoke test on every PR** — the release recipe now lives in a single `make release VERSION=…` target (all four linux/freebsd × amd64/arm64 cross-builds with release ldflags, packaged into `dist/`), used verbatim by `release.yml` on tags and by a new `release-smoke` job in `ci.yml` on every PR, which also asserts the native binary reports the injected version via `--version`. A broken release pipeline is now caught at PR time instead of at tag time (the v0.1.14 tag build broke on a Go-version drift nothing ever exercised). Closes #118.

- **VM-based integration test suite** — `tests/integration` (build tag `integration`, stdlib only) drives a deployed dumpstore instance over HTTP and asserts on real ZFS state: session auth, dataset lifecycle (create / property PATCH / rename / destroy), snapshots with `zfs diff` and clone, per-user quotas, a `zfs send | recv` pipeline followed through the jobs API, and a full pool lifecycle — mirror create, duplicate-name refusal, device offline/online, `zpool replace` with resilver, hot-spare add/remove, export, importable discovery, import — on three dedicated 1 GiB scratch disks the Lima VMs now provision (never on the `tank` data pool). Run locally with `make test-integration`; CI (`.github/workflows/integration-tests.yml`) boots the same Lima VM on a KVM-enabled runner nightly, on manual dispatch, and on PRs labeled `run-integration`. Closes #117.

### Fixed

- **`make install` on Linux now restarts a running service** — the install target used `systemctl enable --now`, which is a no-op when the unit is already active, so redeploys (including `make vm-linux-deploy`) silently kept the old binary running. Now `systemctl enable` + `systemctl restart`, matching what the FreeBSD path already did.
- **Dev VM admin login never worked after provisioning** — `dev/lima-{linux,freebsd}.yaml` wrote a bcrypt `password_hash` into `dumpstore.conf`, but the server rejects bcrypt hashes since the argon2id migration, so `admin`/`admin` on a freshly provisioned VM always failed. The provision scripts now write the correct argon2id PHC hash.

## [v0.1.14] — 2026-06-10

### Added

- **Pool expansion** — each pool card gained an "Expand…" action for growing a pool in place: add a data vdev (single/mirror/raidz1-3, gated behind a confirm-by-typing warning since data vdev additions are irreversible on most layouts), an L2ARC cache device, a SLOG log device (optionally mirrored), or a hot spare — all with the unused-device picker. The vdev tree now renders the auxiliary sections (`logs`, `cache`, `spares`, `special`, `dedup`) with labels, and devices in cache/logs/spares carry a Remove action (`zpool remove`). New endpoints: `POST /api/pools/{pool}/vdevs|cache|log|spare`, `DELETE /api/pools/{pool}/devices/{device}`; new playbooks `zfs_pool_add.yml`, `zfs_pool_remove_device.yml`. Closes #57.

- **Pool lifecycle: create, import, export** — pools can now be created from the UI ("Create Pool" in the Pools tab header) with a structured topology picker (single/stripe, mirror, raidz1-3, draid1-3), a device picker showing only unused block devices, optional `ashift` and root-dataset compression, and a confirm-by-typing gate since creation destroys existing data on the devices. "Import Pool" scans with `zpool import`, lists importable pools with state and advisory status, and supports force-import (`-f`) for unclean exports. Each pool card gained an Export action (`zpool export`) with a busy-pool warning. New endpoints: `POST /api/pools`, `GET /api/pools/importable`, `POST /api/pools/import`, `POST /api/pools/{pool}/export`; new playbooks `zfs_pool_create.yml`, `zfs_pool_import.yml`, `zfs_pool_export.yml`. Closes #23.

- **Per-user / per-group space and quotas** — filesystems gained a Usage row action showing who is consuming space (`zfs userspace` / `zfs groupspace`): per-user/group used bytes, quota, and % used. Quotas can be set or removed inline (`userquota@` / `groupquota@` via the new `zfs_quota_set.yml` playbook); `none` removes the limit. New endpoints: `GET /api/userspace/{name}?kind=user|group`, `POST /api/userquota/{name}`. Closes #25.

- **Snapshot diff** — every snapshot row gained a Diff button that shows what changed between that snapshot and a later snapshot of the same dataset or the live filesystem, via `zfs diff -H`. Results are color-coded by change type (added / removed / modified / renamed), filterable, and capped at 10 000 entries server-side (2 000 rendered at once) so huge diffs stay manageable. New endpoint: `GET /api/snapshots/diff?from=<snap>&to=<snap|empty>`; `zfs diff` runs with an extended 5-minute timeout since busy datasets can take a while. Closes #24.

- **Drive replacement and resilver management** — replace a pool device straight from the vdev tree (`zpool replace` via new `zfs_disk_replace.yml`), with a replacement-device picker backed by the new `GET /api/devices` endpoint (physical block devices from `/sys/block` on Linux / `geom disk list` on FreeBSD, with best-effort "in use by pool" detection). Devices can be taken offline / brought online for maintenance (`zpool offline`/`zpool online` via new playbooks). While a resilver runs the pool card shows a progress bar with live percentage (from the existing poolstatus SSE stream) and a toast announces completion. New endpoints: `GET /api/devices`, `POST /api/pools/{pool}/replace`, `POST /api/pools/{pool}/offline`, `POST /api/pools/{pool}/online`. New package `internal/blockdev`. Closes #55.

- **Dataset rewrite** — the Edit Dataset dialog gained a "Rewrite existing blocks" section that applies updated properties (compression, checksum, copies, …) to already-stored data via `zfs rewrite`. Options: recurse (`-r`), skip snapshot-shared blocks (`-S`), skip clone-shared blocks (`-C`). Because rewriting a large dataset can run for hours, it dispatches through the background jobs manager (`POST /api/rewrite/{name}` returns `202` + `job_id`, progress in the Jobs tab) rather than Ansible. The UI surfaces the caveats: `recordsize` is not applied by rewrite, shared blocks are duplicated unless skipped (pool usage can grow), and rewritten blocks show up in incremental sends. Volumes are rejected (rewrite works through the mounted filesystem). Closes #50.

### Fixed

- **Pipeline jobs no longer lose their final output tail** — `RunPipeline` closed the stdout/stderr pipe read ends before the collector goroutines had drained them, so on loaded machines the buffered tail of a `zfs send | zfs recv` job's output was sometimes discarded (this also made `TestRunPipeline_Success` flaky in CI). The waiter now lets the collectors drain to EOF first, with a bounded grace window before force-closing in case a lingering grandchild keeps a write end open.
- **Auto-snapshot takeover no longer fails on hosts missing zfs-auto-snapshot timers** — the Linux takeover task now enumerates installed `zfs-auto-snapshot-*.timer` units via `systemctl list-unit-files` and only stops/disables units that actually exist, instead of relying on fragile error-message matching. A new op-log task reports the per-timer outcome (stopped and disabled / not present). Closes #93.

### Security

- **Logout cookie now sets `Secure` on TLS** — the session-clearing cookie in `handleLogout` previously omitted the `Secure` attribute (the login cookie already had it). Aligns the clearing cookie with the login cookie. Closes #94.
- **Login error param uses stdlib `html.EscapeString`** — replaced the in-tree `htmlEsc` helper with `html.EscapeString` so static analysers recognise the sanitiser. No behaviour change. Closes #95.
- **GitHub Actions workflows pinned to least privilege** — `ci.yml` and `check-docs.yml` now declare `permissions: contents: read` explicitly instead of inheriting the broader default `GITHUB_TOKEN` scope. Closes #97.

---

## [v0.1.13] — 2026-05-25

### Added

- **Native auto-snapshot management** — dumpstore now executes `com.sun:auto-snapshot:*` snapshots itself via the built-in scheduler instead of delegating to `zfs-auto-snapshot` (Linux) / `zfstools` (FreeBSD). Snapshot naming still follows the legacy `zfs-auto-snap_<bucket>-YYYY-MM-DD-HHMM` convention so existing snapshots are recognised for retention pruning. Inheritance is honoured correctly via `zfs get` — fixes #74 (FreeBSD `zfstools` inheritance bug). New endpoints: `GET /api/auto-snapshot/status`, `POST /api/auto-snapshot/takeover`, `POST /api/auto-snapshot/release`. A status banner above the Datasets tab shows current ownership and offers a one-click "Take over" / "Release" button; takeover disables the OS daemon (systemd timers + `/etc/cron.d/zfs-auto-snapshot` on Linux; `daily_zfs_snapshot_enable` in `periodic.conf` on FreeBSD). New SSE topic `autosnap.status`. Closes #56 and #74.
- **Scheduled ZFS replication** — new Replication tab manages cron-scheduled `zfs send` → `zfs recv` tasks (local and remote-over-SSH targets). Each run snapshots the source as `dumpstore-repl-<UTC>`, places a `dumpstore-repl` hold for the duration of the transfer, picks the most recent common `dumpstore-repl-*` snapshot for an incremental send, dispatches the pipeline via the jobs manager (recv uses `-F -u` to roll the destination forward and skip auto-mount), releases the hold, and prunes the destination to the configured retention count. Orphaned holds from interrupted runs are released on service startup. Per-task run history (last 20) is persisted; manual "Run now" returns a job_id immediately. Two new packages: `internal/scheduler` (minimal 5-field cron parser + 1-minute ticker, no catch-up on missed firings, per-task overlap guard) and `internal/replication` (task store as a single `replication.json` under StateDir, atomic tmp+rename). Endpoints: `GET/POST /api/replication`, `PATCH/DELETE /api/replication/{id}`, `POST /api/replication/{id}/run`, `GET /api/replication/{id}/history`. New SSE topic `replication.update`. Run jobs surface in the Jobs tab tagged `replication.run` / `replication.prune`. Closes #53.
- **Snapshot send/receive** — `POST /api/snapshots/send` dispatches `zfs send [-i prev] [--raw] <snap> | [ssh user@host] zfs recv <target>` as a background job and returns `202 {job_id}` immediately. The pipeline is wired with an OS pipe between two child processes (no shell, no `bash` dependency on FreeBSD); pipefail-equivalent semantics are implemented in the jobs runner. Supports local targets and remote hosts (`user@host` over SSH with `BatchMode=yes`), optional incremental by picking a prior snapshot of the same dataset, and `--raw` for encrypted datasets. UI surfaces the transfer in the Jobs tab with live status, runtime, output tails, and a cancel button. Operator must pre-configure SSH keys for the dumpstore service account. Closes #26.
- **Background jobs runner** (`internal/jobs`) — new manager that runs long-lived data-plane operations as direct child processes outside Ansible, since `zfs send | zfs recv` can run for hours and doesn't fit Ansible's request/response model. Each job is spawned in its own process group; cancel sends SIGTERM and escalates to SIGKILL after 10 s. Bounded 64 KiB stdout/stderr tails are captured in memory. Each job is persisted as a JSON record under `/var/lib/dumpstore/jobs/` (Linux) / `/var/db/dumpstore/jobs/` (FreeBSD); on restart, any record left in `running` state is rewritten to `interrupted`. Auto-prune keeps up to 50 terminal records; explicit `DELETE /api/jobs/{id}` removes a terminal job on demand. New `jobs.update` SSE topic publishes per-job snapshots on every state change. Endpoints: `GET /api/jobs`, `GET /api/jobs/{id}`, `POST /api/jobs/{id}/cancel`, `DELETE /api/jobs/{id}`. New Jobs tab in the UI with Cancel for running jobs and Remove for terminal jobs.

### Changed

- **Password hashing: bcrypt → argon2id** ⚠️ **BREAKING** — existing `password_hash` in `dumpstore.conf` is no longer valid after upgrade. Upgrade path: stop the service, install the new binary, run `dumpstore --set-password`, start the service. Argon2id parameters: `time=3, memory=64 MiB, threads=4` (OWASP minimum). If a bcrypt hash is detected at login, a warning is emitted to the journal. Closes #67.
- **Architecture rule split: configuration writes vs. data-plane writes** — Ansible playbooks are now reserved for idempotent configuration writes (config files, service state, OS resources). Long-running streaming data-plane operations go through the new jobs manager. CLAUDE.md updated accordingly.

### Fixed

- **Batch snapshot delete is per-item tolerant** — one stale `dumpstore-repl` hold or a missing snapshot no longer aborts the whole batch; every item is attempted and any failures are aggregated into a single end-of-batch report with per-item stderr. The handler also pre-releases any `dumpstore-repl` holds for snapshots about to be deleted, so users can clear our snapshots from the UI without first running `zfs release` by hand.

---

## [v0.1.11] — 2026-04-22

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
- **Rename button styling** — `.btn-rename` was missing from `style.css`; the button now matches all other small action buttons (ACL, Chown, NFS, SMB, Snap, iSCSI); closes #87

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
