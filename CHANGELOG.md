# Changelog

All notable changes to this project will be documented here.

## [Unreleased]

### Added

- **Forge SDK check in CI** (#181, Phase 4). A new `forge-sdk` job proves the published spec is SDK-ready.
  - **How it works:** it builds [Forge](https://github.com/cloudflare/forge) from a pinned commit, since Forge isn't on npm yet. It generates the TypeScript SDK from `api/openapi.json`, checks that the client has the API's groups, and type-checks it strictly with TypeScript 7.
  - **Spec changes this needed:**
    - Operations now also carry Forge's native `x-forge-group-name` / `x-forge-method-name`. Forge's generator does not read the `x-fern-sdk-*` keys we emitted, so it produced an empty client.
    - Internal routes are marked `x-forge-skip` and `x-fern-ignore` instead of `x-forge-internal`, a key Forge itself ignores.
    - Path parameter descriptions now say names travel percent-encoded, which generated SDKs do themselves.
  - **Wiki:** the API Reference replaces its hand-written endpoint table with the generated spec, plus how to render it (`npx @redocly/cli build-docs`) and the conventions shared by every operation. The per-route behaviour notes stay.
  - This completes #181.

### Changed (breaking)

- **The API moved to `/api/v1`, with cleaned-up paths** (#181, Phase 3). This is a hard cut: the old paths are gone and there are no aliases.
  - **Path design:** paths are plural nouns, with sub-resources nested under their parent (`/datasets/{name}/acl`). Identities are in the path, not in the body or the query string. Methods follow the resource: PUT sets, DELETE carries no body.
  - **SDK names are unchanged:** each operation keeps its `operationId` and SDK group/method names.
  - **Unchanged paths:** `/login`, `/auth/login`, `/auth/logout`, `/metrics` and `/api/v1/openapi.json`.
  - **One segment per name:** every path parameter is now a single segment, so dataset, snapshot and device names must be **percent-encoded** (`tank/data` → `tank%2Fdata`; `@` → `%40`). A raw slash in a name returns `404`. Reverse proxies must not decode `%2F`; see the wiki's Installation page (nginx: no URI part in `proxy_pass`).
  - **Identifiers that moved into the path:**
    - dataset rename (`{new_name}` body)
    - snapshot clone, send and diff (`?to=`)
    - device replace (`{new_device}` body), offline and online (no body)
    - SMB shares (keyed by share name, `{dataset}` body)
    - Time Machine shares
    - iSCSI target delete (by IQN; the zvol is looked up)
  - **SSH keys:**
    - `GET /api/v1/users/{name}/ssh-keys` returns `{keys: [{key, type, fingerprint, comment}]}` (was an array of strings). Fingerprints match `ssh-keygen -lf`.
    - A key is removed with `DELETE /api/v1/users/{name}/ssh-keys/{fingerprint}` instead of a DELETE carrying the key in its body.
  - **Responses:**
    - `DELETE /api/v1/smb/shares/{name}` returns `{sharename, path, tasks}` (the dataset echo is gone) and `404` for an unknown share.
    - Deleting an unknown iSCSI target is `404` on Linux too.
  - **Metrics:** the `path` label of `http_requests_total` / `http_request_duration_seconds` is now the matched route pattern (`/api/v1/datasets/{name}`). API requests that reach no route are counted as `(unauthenticated)` or `(unmatched)`.
  - **Frontend:** the UI builds every API path with one helper that percent-encodes all interpolated values; a Go test checks every path it uses against the server's routes.

  <details><summary>Migration table (generated from the Phase 2 and Phase 3 specs, joined on operationId)</summary>

| Old | New | operationId |
|---|---|---|
| `GET /api/auth/config` | `GET /api/v1/auth/config` | `authConfig` |
| `POST /api/auth/change-password` | `PUT /api/v1/auth/password` | `authSetPassword` |
| `POST /api/auth/change-username` | `PUT /api/v1/auth/username` | `authSetUsername` |
| `GET /api/whoami` | `GET /api/v1/auth/whoami` | `authWhoami` |
| `POST /api/auto-snapshot/release` | `POST /api/v1/auto-snapshot/release` | `autoSnapshotRelease` |
| `GET /api/auto-snapshot-schedules` | `GET /api/v1/auto-snapshot/schedules` | `autoSnapshotListSchedules` |
| `GET /api/auto-snapshot/status` | `GET /api/v1/auto-snapshot/status` | `autoSnapshotStatus` |
| `POST /api/auto-snapshot/takeover` | `POST /api/v1/auto-snapshot/takeover` | `autoSnapshotTakeover` |
| `GET /api/datasets` | `GET /api/v1/datasets` | `datasetsList` |
| `POST /api/datasets` | `POST /api/v1/datasets` | `datasetsCreate` |
| `GET /api/acl-status` | `GET /api/v1/datasets/acl-status` | `datasetsAclStatus` |
| `DELETE /api/datasets/{name}` | `DELETE /api/v1/datasets/{name}` | `datasetsDelete` |
| `PATCH /api/datasets/{name}` | `PATCH /api/v1/datasets/{name}` | `datasetsUpdate` |
| `GET /api/acl/{dataset}` | `GET /api/v1/datasets/{name}/acl` | `datasetsAclGet` |
| `DELETE /api/acl/{dataset}` | `DELETE /api/v1/datasets/{name}/acl/entries` | `datasetsAclRemoveEntry` |
| `POST /api/acl/{dataset}` | `POST /api/v1/datasets/{name}/acl/entries` | `datasetsAclAddEntry` |
| `GET /api/auto-snapshot/{name}` | `GET /api/v1/datasets/{name}/auto-snapshot` | `datasetsAutoSnapshotGet` |
| `PUT /api/auto-snapshot/{name}` | `PUT /api/v1/datasets/{name}/auto-snapshot` | `datasetsAutoSnapshotSet` |
| `GET /api/chown/{dataset}` | `GET /api/v1/datasets/{name}/owner` | `datasetsOwnerGet` |
| `POST /api/chown/{dataset}` | `PUT /api/v1/datasets/{name}/owner` | `datasetsOwnerSet` |
| `GET /api/dataset-props/{name}` | `GET /api/v1/datasets/{name}/properties` | `datasetsGetProperties` |
| `POST /api/userquota/{name}` | `PUT /api/v1/datasets/{name}/quotas` | `datasetsQuotasSet` |
| `POST /api/datasets/rename` | `POST /api/v1/datasets/{name}/rename` | `datasetsRename` |
| `POST /api/rewrite/{name}` | `POST /api/v1/datasets/{name}/rewrite` | `datasetsRewrite` |
| `GET /api/userspace/{name}` | `GET /api/v1/datasets/{name}/userspace` | `datasetsQuotasUsage` |
| `GET /api/devices` | `GET /api/v1/devices` | `devicesList` |
| `GET /api/events` | `GET /api/v1/events` | `eventsStream` |
| `GET /api/groups` | `GET /api/v1/groups` | `groupsList` |
| `POST /api/groups` | `POST /api/v1/groups` | `groupsCreate` |
| `DELETE /api/groups/{name}` | `DELETE /api/v1/groups/{name}` | `groupsDelete` |
| `PUT /api/groups/{name}` | `PUT /api/v1/groups/{name}` | `groupsUpdate` |
| `POST /api/iscsi/init` | `POST /api/v1/iscsi/init` | `iscsiInit` |
| `GET /api/iscsi/status` | `GET /api/v1/iscsi/status` | `iscsiStatus` |
| `GET /api/iscsi-targets` | `GET /api/v1/iscsi/targets` | `iscsiTargetsList` |
| `POST /api/iscsi-targets` | `POST /api/v1/iscsi/targets` | `iscsiTargetsCreate` |
| `DELETE /api/iscsi-targets` | `DELETE /api/v1/iscsi/targets/{iqn}` | `iscsiTargetsDelete` |
| `GET /api/jobs` | `GET /api/v1/jobs` | `jobsList` |
| `DELETE /api/jobs/{id}` | `DELETE /api/v1/jobs/{id}` | `jobsDelete` |
| `GET /api/jobs/{id}` | `GET /api/v1/jobs/{id}` | `jobsGet` |
| `POST /api/jobs/{id}/cancel` | `POST /api/v1/jobs/{id}/cancel` | `jobsCancel` |
| `GET /api/v1/openapi.json` | unchanged | `metaOpenapi` |
| `GET /api/operations/{id}` | `GET /api/v1/operations/{id}` | `operationsGet` |
| `GET /api/pools` | `GET /api/v1/pools` | `poolsList` |
| `POST /api/pools` | `POST /api/v1/pools` | `poolsCreate` |
| `POST /api/pools/import` | `POST /api/v1/pools/import` | `poolsImport` |
| `GET /api/pools/importable` | `GET /api/v1/pools/importable` | `poolsListImportable` |
| `GET /api/iostat` | `GET /api/v1/pools/iostat` | `poolsIostat` |
| `GET /api/scrub-schedules` | `GET /api/v1/pools/scrub-schedules` | `poolsScrubListSchedules` |
| `GET /api/poolstatus` | `GET /api/v1/pools/status` | `poolsStatus` |
| `POST /api/pools/{pool}/cache` | `POST /api/v1/pools/{pool}/cache` | `poolsDevicesAddCache` |
| `DELETE /api/pools/{pool}/devices/{device}` | `DELETE /api/v1/pools/{pool}/devices/{device}` | `poolsDevicesRemove` |
| `POST /api/pools/{pool}/offline` | `POST /api/v1/pools/{pool}/devices/{device}/offline` | `poolsDevicesOffline` |
| `POST /api/pools/{pool}/online` | `POST /api/v1/pools/{pool}/devices/{device}/online` | `poolsDevicesOnline` |
| `POST /api/pools/{pool}/replace` | `POST /api/v1/pools/{pool}/devices/{device}/replace` | `poolsDevicesReplace` |
| `POST /api/pools/{pool}/export` | `POST /api/v1/pools/{pool}/export` | `poolsExport` |
| `POST /api/pools/{pool}/log` | `POST /api/v1/pools/{pool}/log` | `poolsDevicesAddLog` |
| `DELETE /api/scrub/{pool}` | `DELETE /api/v1/pools/{pool}/scrub` | `poolsScrubCancel` |
| `POST /api/scrub/{pool}` | `POST /api/v1/pools/{pool}/scrub` | `poolsScrubStart` |
| `DELETE /api/scrub-schedule/{pool}` | `DELETE /api/v1/pools/{pool}/scrub-schedule` | `poolsScrubRemoveSchedule` |
| `PUT /api/scrub-schedule/{pool}` | `PUT /api/v1/pools/{pool}/scrub-schedule` | `poolsScrubSetSchedule` |
| `POST /api/pools/{pool}/spare` | `POST /api/v1/pools/{pool}/spare` | `poolsDevicesAddSpare` |
| `POST /api/pools/{pool}/vdevs` | `POST /api/v1/pools/{pool}/vdevs` | `poolsVdevsAdd` |
| `GET /api/replication` | `GET /api/v1/replication/tasks` | `replicationList` |
| `POST /api/replication` | `POST /api/v1/replication/tasks` | `replicationCreate` |
| `DELETE /api/replication/{id}` | `DELETE /api/v1/replication/tasks/{id}` | `replicationDelete` |
| `PATCH /api/replication/{id}` | `PATCH /api/v1/replication/tasks/{id}` | `replicationUpdate` |
| `GET /api/replication/{id}/history` | `GET /api/v1/replication/tasks/{id}/history` | `replicationHistory` |
| `POST /api/replication/{id}/run` | `POST /api/v1/replication/tasks/{id}/run` | `replicationRun` |
| `GET /api/services` | `GET /api/v1/services` | `servicesList` |
| `POST /api/services/{service}/{action}` | `POST /api/v1/services/{service}/{action}` | `servicesControl` |
| `DELETE /api/smb/homes` | `DELETE /api/v1/smb/homes` | `smbHomesDisable` |
| `GET /api/smb/homes` | `GET /api/v1/smb/homes` | `smbHomesGet` |
| `POST /api/smb/homes` | `PUT /api/v1/smb/homes` | `smbHomesSet` |
| `POST /api/smb/init` | `POST /api/v1/smb/init` | `smbInit` |
| `GET /api/smb-shares` | `GET /api/v1/smb/shares` | `smbSharesList` |
| `DELETE /api/smb-share/{dataset}` | `DELETE /api/v1/smb/shares/{name}` | `smbSharesDelete` |
| `POST /api/smb-share/{dataset}` | `PUT /api/v1/smb/shares/{name}` | `smbSharesSet` |
| `GET /api/smb/status` | `GET /api/v1/smb/status` | `smbStatus` |
| `GET /api/smb/timemachine` | `GET /api/v1/smb/timemachine` | `smbTimeMachineList` |
| `DELETE /api/smb/timemachine/{sharename}` | `DELETE /api/v1/smb/timemachine/{name}` | `smbTimeMachineDelete` |
| `POST /api/smb/timemachine` | `PUT /api/v1/smb/timemachine/{name}` | `smbTimeMachineSet` |
| `GET /api/smb-users` | `GET /api/v1/smb/users` | `smbUsersList` |
| `DELETE /api/smb-users/{name}` | `DELETE /api/v1/smb/users/{name}` | `smbUsersRemove` |
| `POST /api/smb-users/{name}` | `PUT /api/v1/smb/users/{name}` | `smbUsersAdd` |
| `POST /api/smb/wsdd` | `POST /api/v1/smb/wsdd/apply` | `smbWsddApply` |
| `GET /api/snapshots` | `GET /api/v1/snapshots` | `snapshotsList` |
| `POST /api/snapshots` | `POST /api/v1/snapshots` | `snapshotsCreate` |
| `POST /api/snapshots/delete-batch` | `POST /api/v1/snapshots/batch-delete` | `snapshotsDeleteBatch` |
| `DELETE /api/snapshots/{snapshot}` | `DELETE /api/v1/snapshots/{snap}` | `snapshotsDelete` |
| `POST /api/snapshots/clone` | `POST /api/v1/snapshots/{snap}/clone` | `snapshotsClone` |
| `GET /api/snapshots/diff` | `GET /api/v1/snapshots/{snap}/diff` | `snapshotsDiff` |
| `POST /api/snapshots/send` | `POST /api/v1/snapshots/{snap}/send` | `snapshotsSend` |
| `GET /api/sysinfo` | `GET /api/v1/system/info` | `systemInfo` |
| `GET /api/network` | `GET /api/v1/system/network` | `systemNetwork` |
| `GET /api/schema` | `GET /api/v1/system/schema` | `systemSchema` |
| `GET /api/smart` | `GET /api/v1/system/smart` | `systemSmart` |
| `GET /api/version` | `GET /api/v1/system/version` | `systemVersion` |
| `GET /api/tls/status` | `GET /api/v1/tls` | `tlsGet` |
| `PATCH /api/tls/config` | `PATCH /api/v1/tls` | `tlsUpdate` |
| `POST /api/tls/acme/issue` | `POST /api/v1/tls/acme/issue` | `tlsAcmeIssue` |
| `POST /api/tls/acme/renew` | `POST /api/v1/tls/acme/renew` | `tlsAcmeRenew` |
| `POST /api/tls/gencert` | `POST /api/v1/tls/self-signed` | `tlsGenerateSelfSigned` |
| `GET /api/users` | `GET /api/v1/users` | `usersList` |
| `POST /api/users` | `POST /api/v1/users` | `usersCreate` |
| `DELETE /api/users/{name}` | `DELETE /api/v1/users/{name}` | `usersDelete` |
| `PUT /api/users/{name}` | `PUT /api/v1/users/{name}` | `usersUpdate` |
| `GET /api/users/{name}/sshkeys` | `GET /api/v1/users/{name}/ssh-keys` | `usersSshKeysList` |
| `POST /api/users/{name}/sshkeys` | `POST /api/v1/users/{name}/ssh-keys` | `usersSshKeysAdd` |
| `DELETE /api/users/{name}/sshkeys` | `DELETE /api/v1/users/{name}/ssh-keys/{fingerprint}` | `usersSshKeysRemove` |
| `POST /auth/login` | unchanged | `authLogin` |
| `POST /auth/logout` | unchanged | `authLogout` |
| `GET /login` | unchanged | `authLoginPage` |
| `GET /metrics` | unchanged | `metricsScrape` |

  </details>

- **Every API route has typed request and response schemas** (#181, Phase 2). All JSON handlers now run on the typed adapter (`func(ctx, In) (Out, error)`), so the OpenAPI spec's parameter, body and response schemas come from the Go signatures and can't drift from the code.
  - **No generic schemas left:** 71 responses and 36 request bodies that were documented as generic JSON objects now have named schemas (e.g. `CreatePoolRequest`, `PoolResult`, `SMBHomes`, `ISCSITargetResult`). `TestNoUntypedRoutes` keeps it that way, and optional request fields are marked so the spec's `required` lists are accurate.
  - **Error statuses:** each operation now documents the 4xx statuses it can return.
  - **Integration suite:** it records every JSON response it receives and checks each one, including write and error responses, against the served spec. Any 4xx status an operation doesn't document fails the run.
  - **Wire format unchanged:** paths, status codes, JSON keys and values stay the same. Response shapes were compared route by route against the previous build on both dev VMs.
  - **Deliberate differences:**
    - A malformed JSON body now always gets `invalid request body: <detail>`; a few handlers used to say `invalid JSON`.
    - The body is decoded before the handler's own checks, so a request that is malformed *and* invalid in another way (unknown dataset, Samba not initialised, …) now gets that 400 rather than the 404/409 it used to.
    - Response keys follow struct order instead of alphabetical order.

- **OpenAPI 3.1 spec generated from the route registry** (#181, Phase 1) — every route is now registered through `internal/apispec` together with its metadata (SDK group and method name for Forge/Fern, summary, parameter, request and response types, success status, auth), and the spec is generated from those same registrations, so it cannot drift from what the server serves. It is served at `GET /api/v1/openapi.json` (authenticated like the rest of `/api`) and committed as `api/openapi.json`. `make openapi` regenerates it; `go test` fails when the committed file is stale, and a new CI job lints it with Redocly CLI (`recommended-strict`, config in `redocly.yaml`). Schemas are derived by reflection from the Go types (JSON tags, `omitempty`, nullability of pointers/slices/maps, `time.Time`, embedded structs, `doc:`/`enum:` tags); 37 named types become `components.schemas` with SDK-friendly names (`ReplicationTask`, `ISCSITarget`, `SmartReport`, …). Every write documents the optional `X-Operation-ID` header and the shared `Error` response; the session cookie and trusted-proxy `X-Remote-User` are the security schemes. 28 GET handlers moved to a typed adapter (`func(ctx, Params) (Resp, error)`), so their spec types come from the compiler; the remaining routes declare their types by hand where named types exist. 71 responses and 36 request bodies are still untyped (`map[string]any` / anonymous structs, documented as generic JSON) and pinned in a burn-down list for Phase 2. No API change: paths, status codes and bodies are identical (verified endpoint by endpoint against the previous build on the dev VMs); the integration suite additionally validates every parameterless GET response against its published schema. The `/api/v1` path cleanup is Phase 3.

- **Operation IDs for write progress** (#184) — every API write (`POST`/`PUT`/`PATCH`/`DELETE` under `/api/`) is now an *operation* with an ID: chosen by the client via the `X-Operation-ID` request header (8–64 chars of `[A-Za-z0-9-]`; invalid → `400`, already used → `409` with `duplicate_operation: true`) or generated by the server, and always echoed in the response header. Steps on the `ansible.progress` SSE topic now carry `op` and a per-operation `seq` (1, 2, 3, …), so the op-log shows only its own operation's steps — before, any write by another tab or client appeared in every open op-log. The new `GET /api/operations/{id}?after=<seq>` returns an operation's state, its steps after `seq`, and the final HTTP status and response body. The frontend uses it to fill gaps after an SSE reconnect and to recover the result of a write whose HTTP response was lost (it polls until the operation finishes, up to 5 min) — including the case where the browser silently retransmits a request after a connection reset, which the duplicate-ID check now refuses instead of running twice. Records are kept in memory only: running operations always, finished ones for up to 1 h and at most 200 (results over 256 KiB are not stored). Final HTTP responses, cached SSE topics, and `jobs.update` are unchanged.

### Fixed

- **Small defects from the docs audit** (#197).
  - **SSH key add failed for users without a same-named group.** `~/.ssh` and `authorized_keys` now get the user's primary GID.
  - **`mountpoint=legacy` was treated as a relative path.** ACL, chown and SMB checks treat it like `none`: GET and PUT `/datasets/{name}/owner` and ACL writes return `400`; `smb_usershare_set.yml` refuses it too.
  - **Single snapshot `DELETE` failed on held replication snapshots** ("dataset is busy"). It releases dumpstore's replication hold first, as batch delete does.
  - **FreeBSD auto-snapshot takeover/release used `lineinfile` on `periodic.conf`, and release didn't restore `periodic.conf.local`.**
    - Takeover reads `{hourly,daily,weekly,monthly}_zfs_snapshot_enable` (the switches of the `zfs-periodic` package's periodic scripts) from `/etc/periodic.conf`, `/etc/periodic.conf.local` and `/usr/local/etc/periodic.conf`. It records every `YES` it finds in `/var/db/dumpstore/autosnap-disabled-periodic.json` and sets it to `NO` with `sysrc -f`.
    - Release restores exactly the recorded values. sysrc rewrites them quoted: `x=YES` comes back as `x="YES"`. Without a record (a takeover by an older dumpstore), release sets `daily_zfs_snapshot_enable="YES"` in `/etc/periodic.conf`, as before.
    - Detection reads all three files and all four switches, a later file overriding an earlier one.
  - **A second *Generate self-signed* with another hostname kept the old certificate.** Every request now writes a new one.
  - **A partly failed batch or recursive snapshot delete skipped the SSE update**, although some snapshots were gone. `snapshot.query` is published on failure too.
  - **SSH key add/remove accepted `nobody` and `nfsnobody`.** They now return `403`, like the other user writes.
  - **`POST /api/v1/users` with `smb_user: true` and no password returned `201`** and silently skipped Samba. It returns `400 smb_user requires a password`.
  - **The user/group playbooks asserted a fixed UID/GID `>= 1000`,** while the handlers use `UID_MIN` from `/etc/login.defs`. The handlers now pass that limit as `uid_min`.
  - **Login rate limiter:**
    - It counted successful logins and never pruned its per-IP buckets. Now only failed logins count (a success resets the IP), and expired buckets are pruned at most once per window.
    - When blocked, clients sending `Accept: application/json` get `429` with `Retry-After`; browsers are still redirected to `/login?error=…`.
  - **`smartctl` ran without a timeout.** It now has the 30 s timeout of the ZFS reads.
  - Stale comments in `auditLog` and the `smb_init.yml` header were corrected.
- **HTTP→HTTPS redirect dropped a non-default TLS port.** With `--tls --tls-port 8443`, `http://host:<http-port>/x` redirected to `https://host/x`, i.e. port 443, where nothing listens. The redirect now targets `https://host:8443/x`; with the default port 443 it stays `https://host/x`. IPv6 hosts keep their brackets.
- **FreeBSD: dumpstore flags could not be set from `rc.conf`** (#191). The rc.d script built a fixed argument list, so `--tls`, `--tls-port`, `--http-port`, `-debug` and `-log-stdout=false` could not be enabled; `dumpstore_flags` doesn't help because rc.subr hands it to `daemon(8)`. New `dumpstore_args` variable, appended to dumpstore's own arguments: `sysrc dumpstore_args="--tls"`.

- **FreeBSD: SMB config writes failed while Samba was stopped** (#196). `service samba_server reload` fails when Samba isn't running, so every share, homes or Time Machine write and `POST /api/v1/smb/init` failed until Samba was started from the Services tab. `smb_apply.yml` now checks each service with the OS's own status command (`systemctl is-active` / `service … status`), reloads the ones that are running and starts the ones that are stopped. Ansible's `service` module is no longer used for this. On FreeBSD it probes with `onestatus` and starts with `onestart`, which for `samba_server` force-starts `winbindd` (not enabled without `idmap uid`), misreads a running Samba as stopped and leaves an orphaned `winbindd` that blocks every later start.

- **Security: no-password loopback safeguard bypassed with `--tls`** (#192). Without a password dumpstore binds `127.0.0.1:8080`, but with `--tls` the HTTPS and redirect listeners bound all interfaces. They now stay on loopback too until a password is set.
- **Security: TLS cert paths could inject command options** (#194). `cert_dir` was interpolated unquoted into the openssl/lego command strings, and the path check allowed whitespace, so a path with a space or newline split into extra options run as root (admin-only). The TLS playbooks now pass arguments as `argv`, and cert/key paths and `cert_dir` must be plain absolute paths (`A-Z a-z 0-9 . _ / -`). The other playbooks that build command strings were audited and are not affected: passwords reach `smbpasswd` via stdin, and the home-directory move only uses the allow-listed `home`.
- **Security: `whoami` trusted `X-Remote-User` from any client** (#195). It now honours the header only from `trusted_proxies`, through the same identity helper the auth middleware uses; otherwise it reports the session user.

- **The SSE stream was traced again since the `/api/v1` move.** The OTEL tracing filter still excluded the old `/api/events` path, so with OTLP enabled every open browser tab produced a server span lasting hours. The filter now excludes `/api/v1/events`, and `TestTraceRequest` checks that the excluded path is the SSE route the server actually registers. (#181 Phase 3 regression)

- **`POST /api/snapshots/clone` and `POST /api/replication` answered 201 with `Content-Type: text/plain`** — the status was written before the JSON content type was set. Both now send `application/json`. (#181 Phase 2)
- **The OpenAPI spec documented `POST /api/groups` as 200**; it returns 201. Found by the new response conformance check. (#181 Phase 2)
- **FreeBSD: dumpstore fully owns `/etc/ctl.conf`** (#186) — the iSCSI playbooks block-patched `ctl.conf`, which broke in several ways. Every target got its own `portal-group` on the same address, so with a second target ctld's reload failed with `bind … Address already in use`, and after a restart or reboot ctld refused to start at all — taking every target down — while the op-log still reported success, because `service ctld reload` only sends SIGHUP. Targets with auth *none* got no auth clause and fell back to ctld's predefined `default` auth group, which denies all access, and portal groups denied discovery the same way: "no auth" targets were never reachable. IPv6 portals were written as `:::3260`. Now `ctl.conf` is rendered whole from `internal/iscsi/ctl.conf.tmpl` on every write: one portal group per listen address (IPv6 bracketed), discovery open but filtered to what each initiator may reach, `auth-group no-authentication` / `auth-type none` + `initiator-name` / `chap` per target, mode `0600` with no backup copies of the CHAP secrets. A one-time *Initialize ctld* (`POST /api/iscsi/init`, gate status at `GET /api/iscsi/status`) backs up the existing file to `/etc/ctl.conf.pre-dumpstore` and imports the targets the model can represent, including those from earlier dumpstore versions; anything else is listed in the op-log as not imported. Until then, target writes return `409`. After reloading, the playbook verifies `ctladm portlist` and ctld's listening sockets; if the change did not take effect (e.g. a portal IP not assigned to the host) the operation fails with the reason and the previous file is restored. Creating a duplicate IQN or exporting an already-exported zvol returns `409` (before, the file was silently overwritten), deleting an unknown target `404`, a missing zvol device `400`, and CHAP credentials may no longer contain `"` or `\` (ctl.conf strings have no escapes). Linux (targetcli) is unchanged.
- **Deleting a dataset no longer leaves dangling shares, iSCSI targets, or replication tasks** (#183) — `DELETE /api/datasets/{name}` (and recursive deletes of a subtree), `POST /api/datasets/rename`, and a `mountpoint` change via `PATCH /api/datasets/{name}` are now refused with `409` while dumpstore-managed config still refers to an affected dataset: SMB usershares, the `[homes]` base path, Time Machine shares, iSCSI targets on a zvol, and replication tasks (exact source, or local target). Shared paths are resolved to the dataset that actually holds them (longest mountpoint match on path-component boundaries, so `/tank/data2` is not attributed to `tank/data`). The response carries a structured `conflicts` list, which the op-log dialog renders as a list of blockers. The check fails closed — an unreadable `smb.conf`, usershare directory, or iSCSI config aborts with `500 reference check failed` instead of skipping the check — and runs under one lock shared with every handler that adds a reference, so nothing can be added between check and destroy. Previously the usershare survived the delete invisibly and re-activated, with its original ACL (`Everyone: Full Control`), on any new dataset created at the same path; replication tasks failed on every run; and an iSCSI-backed zvol failed with an unexplained `500`.
- **FreeBSD dev VM provisions Samba; iSCSI integration tests run on ctld** — `dev/lima-freebsd.yaml` installs `samba416` (the ports tree's `SAMBA_DEFAULT`) and sets `IGNORE_OSVERSION=yes`, without which a fresh 15.0 image can't open the quarterly repo built on a newer 15.x. `TestSMBSurface`, `TestDatasetRefGuard`, and `TestISCSITarget` now run on FreeBSD instead of skipping; the iSCSI tests share an `iscsiFixture` that backs up and restores `/etc/ctl.conf`, `ctld_enable`, and ctld's running state.
- **Stale SMB usershares are visible and removable** (#183) — `GET /api/smb-shares` now reads the usershare files directly instead of `net usershare info`, so shares whose path no longer exists (left over from a delete before this fix, or a dataset destroyed outside dumpstore) are listed with `path_missing: true`. The Users & Groups tab shows them in a *Stale SMB Shares* section with a *Remove* button.

### Changed

- **`/metrics` now served from the OpenTelemetry meter** (#168, phase 3 of #49) — every dumpstore metric is recorded once on the OTEL meter API: HTTP request counters/latency, Ansible and in-process op run counters/durations, Go runtime stats, and build info. The meter provider now always runs. Its Prometheus exporter reader (private registry, `UnderscoreEscapingWithSuffixes` pinned, no `target_info`, no `otel_scope_*` labels) serves `GET /metrics`, and when an OTLP endpoint is configured, an OTLP reader on the same provider pushes the same instruments. Before, OTLP carried only Go runtime and `http.server.*` metrics. The four hand-written Prometheus text emitters (their own mutexes, maps and bucket code) are gone. The `/metrics` contract is unchanged: same metric names, types, label keys, bucket boundaries, `# HELP` strings and `text/plain; version=0.0.4` content type, pinned by a golden test (`metrics_test.go`) and checked against `main` on the Linux and FreeBSD VMs. Differences: series come out sorted rather than in map order; a family with no samples yet (e.g. `ansible_runs_total` before the first playbook run) is omitted instead of printing bare `# HELP`/`# TYPE` lines (no series is lost); the content type gains promhttp's `escaping=underscores` parameter (`text/plain; version=0.0.4; charset=utf-8; escaping=underscores`); with OTLP enabled, `/metrics` additionally shows the OTEL runtime (`go_memory_*`, `go_goroutine_count`, …) and otelhttp (`http_server_request_duration_seconds`, …) series, because they share the provider (additions only); and if OTEL initialisation fails at startup (e.g. an invalid `OTEL_*_EXPORTER` value), `/metrics` answers `503` instead of serving data. `go.opentelemetry.io/otel/exporters/prometheus`, `github.com/prometheus/client_golang` and `github.com/prometheus/otlptranslator` became direct dependencies (all were already in the module graph via autoexport). Closes #168.
- **Documentation brought level with the code** — new wiki page *Observability* (Prometheus metric reference with labels, every OTEL variable, span names and attributes, local log and audit-line format); *Installation* gained a TLS / HTTPS section (how `--tls`, `--tls-port`, and `--http-port` interact, that the certificate is loaded at startup, and that ACME renewal is not automated) and a command-line flag table (`-debug`, `-tls-port`, `-http-port` were undocumented); the *API Reference* now has a detail section for all 112 routes (55 were only listed in the overview table: authentication, TLS, system reads, users/groups/SSH keys, SMB shares and users, chown, snapshot clone/batch-delete, auto-snapshot takeover/release). Corrected along the way: the snapshot `DELETE ?recursive=true` (destroys same-named snapshots in descendants, not clones), the auto-snapshot intro (dumpstore runs its own opt-in scheduler; the OS daemon is opt-out), pool/device/scrub operations that run in-process rather than via Ansible, the SMB init gate (it only checks that `smb.conf` exists), TLS "reload" (it is a restart), and the docs site's ACME "renews automatically" claim, stale planned list (still showing the cancelled log viewer), and incomplete requirements table. FEATURES.md no longer points at a non-existent `contrib/observability/`; lldap (#62) and NUT (#52) moved to Cancelled.
- **README trimmed to an overview (1,039 → 126 lines)**; the details live in the wiki, which was brought up to date. Architecture, the route map, the API reference, the project layout, observability setup, and per-distro requirements moved to the wiki's Installation, Architecture, and API Reference pages; the stale "Planned" table was replaced by a link to FEATURES.md. Corrected along the way: argon2id (not bcrypt) password hashes, Go 1.26+, Samba reloads instead of restarting, the *Initialize Samba* flow (the old `smb_setup.yml` no longer exists), FreeBSD ACLs being NFSv4-only, and playbook names for operations that moved to the in-process ops lane. The wiki API table now lists all 109 registered routes (25 were missing) and all 13 SSE topics; FEATURES.md gained the missing audit-logging row.
- **Screenshots regenerated for the current UI** — 18 captures covering every tab and the main dialogs (previously 8, from before the v0.2.0 redesign), produced by the new `make screenshots`: `dev/screenshots/seed.sh` seeds the Linux dev VM with demo data through the API (and removes it again), `dev/screenshots/capture.mjs` drives headless Chrome via `puppeteer-core`. The README shows eight; the new wiki *Screenshots* page shows all.

## [v0.2.3] — 2026-10-19

### Fixed

- **Services tab no longer offers actions for services that aren't installed** — when no candidate unit existed (e.g. `wsdd` installed without Debian/Ubuntu's `wsdd-server`, or no iSCSI target stack), the row fell back to a unit name that doesn't exist, showed it as inactive with Start/Enable buttons, and every action failed in Ansible with *Could not find the requested service*. `GET /api/services` now reports `installed` per service; such rows show *not installed* without actions, and `POST /api/services/{name}/{action}` returns `409` instead of running the playbook.

## [v0.2.2] — 2026-10-17

### Added

- **Windows discovery (wsdd) management** — wsdd only announces the host and its workgroup, so dumpstore now treats it as part of the Samba config it owns instead of a separate settings surface. On Linux the options file is located from the unit itself (`EnvironmentFile` of a unit whose `ExecStart` expands `$WSDD_PARAMS` — `/etc/default/wsdd` for Debian/Ubuntu's `wsdd-server`) and rendered from `smb.conf` on every Samba write, so Windows sees the same workgroup Samba serves; a running wsdd is restarted only when the file changed, a stopped one is left stopped. On FreeBSD the port's rc.d script already reads the workgroup from `smb4.conf`, so nothing is rendered. The Services tab gains a wsdd row (`wsdd-server` → `wsdd` on Linux, `wsdd` on FreeBSD); the SMB section shows wsdd status, a distro install hint when missing, and a *Manage* action (`POST /api/smb/wsdd`) for hosts where wsdd was installed after Samba was initialised. `GET /api/smb/status` carries the `wsdd` block. Closes #86.

### Fixed

- **Background-worker log lines carry trace ids** — auto-snapshot (snapshot/prune failures, tick summary) and replication (prune results, run-record write failures) logged without the span context, so those lines lacked `trace_id`/`span_id` in journald and OTLP. They now use the `*Context` slog variants. Last open item of #49.

## [v0.2.1] — 2026-10-10

### Added

- **Integration coverage for most API endpoints** (#141) — the Lima VM suite grew from ~17 paths to most of the API: all read endpoints and SSE, users/groups/SSH keys, Samba (init, usershares, users, homes, Time Machine), POSIX ACLs, chown, iSCSI targets, replication CRUD + run + history, auto-snapshot properties, scrub schedules, service control, TLS gencert/config, and password change — with negative cases (400/401/403/404/409) throughout. The Linux dev VM now provisions `acl samba nfs-kernel-server targetcli-fb zfs-auto-snapshot`; tests skip when a tool is missing. Fixtures are cleaned up before and after each test and rewritten host config is restored byte-for-byte; the suite is green twice in a row on both the Linux and FreeBSD VMs with the host left unchanged. Building it surfaced the fixes listed under Fixed (password change, replication runs, iSCSI IQN, FreeBSD users/groups/chown) plus #152 (autosnap takeover on cron-based Debian) and #156 (FreeBSD ACLs).

- **OpenTelemetry instrumentation (phases 1+2 of #49)** — driven entirely by the standard OTLP env vars (`OTEL_EXPORTER_OTLP_ENDPOINT`, `…_PROTOCOL` with `http/protobuf` default and `grpc` supported, `OTEL_SERVICE_NAME`, `OTEL_SDK_DISABLED`); every OTEL code path is a no-op while they are unset. When enabled, dumpstore exports: **traces** — a root span per API request (named `METHOD /route`, tagged with the journald `req_id`), child spans for every Ansible playbook run (extra-var keys only, never values) and every ops-lane `zfs`/`zpool` command, and root spans for jobs (linked to the dispatching request), scheduled replication runs, and auto-snapshot fires; **logs** — slog now feeds the OTEL log SDK as a single producer whose journald exporter reproduces the classic format byte-for-byte (golden-tested) while an OTLP branch ships the same records with `trace_id`/`span_id` correlation (journald lines gain trace ids too); **metrics** — Go runtime and `http.server.*` metrics via OTLP push, with the Prometheus `/metrics` endpoint unchanged. Write-path runners (`ansible`, `ops`) now take a context for span parenting but deliberately drop cancellation — a client disconnect cannot kill a mutating command. The System tab's host info card shows the effective OTEL status (enabled badge, endpoint, protocol, service name — read-only, env-driven). `-log-stdout=false` disables the local journald/logfile branch for OTLP-only logging (warns at startup if that would discard all logs). First non-`golang.org/x` dependency (CNCF-governed OTEL SDK); CONTRIBUTING/CLAUDE dependency rule updated. ZFS read-path spans and the Prometheus→OTEL meter migration (phase 3) remain on #49.

- **Installed Software card shows requirement tiers** — each probed tool now carries `required`/`feature` metadata; the System tab table gained a "Needed for" column (Core for ZFS/Ansible/Python, otherwise the feature the tool unlocks — SMB shares, iSCSI targets, Windows share discovery, …), and a missing required tool renders a red "missing" badge instead of the same grey N/A as optional tools. Closes #135.
- **FreeBSD warning when the ZFS kernel module is not loaded** — `platformWarnings()` runs `kldstat -q -m zfs` (also matches compiled-in modules) and surfaces a System-panel warning with the fix (`kldload zfs` / `zfs_enable=YES`), since userland binaries can be present while the module is not loaded. Part of #136.

### Fixed

- **Services panel controls the iSCSI target, not the initiator** — on Linux the "iSCSI Target" service mapped to `iscsid` (the open-iscsi initiator), so it showed inactive while targets were served and start/stop/restart/enable acted on the wrong daemon. It now resolves to the LIO restore unit: `rtslib-fb-targetctl` (Debian/Ubuntu), falling back to `target`. Closes #159.
- **Several quick SMB edits no longer take Samba down** — every smb.conf write (init, `[homes]`, Time Machine) fully restarted smbd/nmbd. A handful of edits within ~10 s exceeded systemd's start limit, leaving `smbd` `failed (start-limit-hit)` until someone intervened; every write also dropped connected clients. `smb_apply.yml` now reloads the services instead (smbd/nmbd re-read smb.conf on SIGHUP; a stopped unit is started).
- **Auto-snapshot takeover/release works with cron-based zfs-auto-snapshot** (Debian/Ubuntu) — takeover only deleted `/etc/cron.d/zfs-auto-snapshot`, so the `cron.{hourly,daily,weekly,monthly}` scripts kept snapshotting alongside dumpstore's scheduler (duplicates) while status claimed the OS daemon was inactive; release only knew systemd timers, returned 500, and never restored the deleted file. Takeover now renames all five cron entry points to `<file>.dumpstore-disabled` (cron.d and run-parts skip dotted names) and records which systemd timers it disabled; release renames the cron files back and re-enables exactly the recorded timers, never touching units that are not installed. Detection counts every cron entry point. Closes #152.
- **NFSv4 ACLs work on FreeBSD** — every ACL endpoint shelled out to Linux's `nfs4_getfacl`/`nfs4_setfacl` (nfs4-acl-tools), so on FreeBSD — where ZFS datasets default to `acltype=nfsv4` — ACL reads and writes returned 500. FreeBSD now uses its base `getfacl`/`setfacl`, translated to and from the same `type:flags:principal:perms` entry form the API and UI already use (permission/flag letters mapped, `g` flag ↔ `group:` principal, `OWNER@` ↔ `owner@`). Also fixed for both platforms: adding an NFSv4 entry first ran `zfs set acltype=nfsv4acls`, a value current OpenZFS rejects on Linux and FreeBSD alike — it now sets `nfsv4`. Closes #156.
- **Startup now fails fast when ZFS is missing** — `checkDeps()` verifies `zfs` and `zpool` are in PATH alongside the existing `ansible-playbook` check, instead of starting a server where every tab errors at runtime. Closes #136.
- **`GET /api/sysinfo` no longer re-runs every software probe per request** — the Installed Software probes (~13 external `--version` execs) and platform warnings are cached for 5 minutes; concurrent requests during a refresh share one probe round. Tracing showed p95 ≈ 4 s while the frontend polls sysinfo every 60 s per tab. Response shape unchanged. Closes #139.
- **No more bare `GET` spans for requests that never reach a route** — auth-middleware 401s are now named `METHOD (unauthenticated)` and unrouted requests `METHOD (unmatched)`, each with a `dumpstore.short_circuit` attribute (`auth` / `unmatched`); the raw URL path is never used, keeping span names low-cardinality. Closes #140.
- **Change password works again** — `POST /api/auth/change-password` still verified and stored **bcrypt** hashes, while login has only accepted argon2id since #88: on any normally provisioned instance it always answered 401, and had it succeeded it would have written a hash login refuses (admin lockout). It now uses the same argon2id helpers as login and `--set-password`.
- **Manual replication runs record history and release their hold** — `POST /api/replication/{id}/run` passed the request context to the runner, whose post-job watcher exits when that context is cancelled — i.e. as soon as the handler returned. Manual runs therefore never wrote a history record, never pruned the target, and left a `dumpstore-repl` hold on the source snapshot (blocking its destroy until the next service restart). The run is now detached from the request (`context.WithoutCancel`), matching the Ansible/ops runners.
- **iSCSI targets on Linux now show their IQN** — the targetcli `saveconfig.json` parser read the target name from a `name` key, but targetcli stores it under `wwn`. Every Linux target was listed with an empty IQN, so the UI could not display it and deleting a target from the UI sent an empty IQN (rejected with 400). Covered by a parser test against a real targetcli-fb saveconfig.
- **Setting a user password works on FreeBSD** — `user_create.yml` and `user_modify.yml` set passwords with `chpasswd`, which FreeBSD does not have: creating a user with a password (or changing one) failed with a 500 at the "Set user password" step, after the account itself had already been created. FreeBSD now uses `pw usermod <user> -h 0` (password on stdin); Linux keeps `chpasswd`.
- **Dataset ownership is readable on FreeBSD** — `GET /api/chown/{dataset}` ran GNU `stat -L --format=%U %G`, which FreeBSD's BSD `stat` rejects (500). Ownership is now read via the stat syscall and resolved with `os/user`, identical on both platforms (numeric ids when there is no passwd/group entry, as before).
- **Group members and rename work on FreeBSD** — `group_modify.yml` used Linux-only `gpasswd -M` and `groupmod -n`; on FreeBSD every group edit failed at "Set members" (500). FreeBSD now uses `pw groupmod -M` / `-l`; both tasks run argv-based instead of through a shell.

## [v0.2.0] — 2026-07-13

### Changed

- **All dialogs are now right-anchored slide-in panels** — every `<dialog>` slides in from the right edge full-height with the same animation and shadow as the dataset drawer, for one consistent interaction pattern; footer buttons pin to the panel bottom. The form-field styling (dark inputs/selects, full-width layout, focus accents) was scoped to `dialog` selectors and never reached the drawer, leaving its fields with browser-default colors and misaligned widths — the rules now cover `.drawer` too.

- **Snapshots tab grouped by dataset** (part 2 of #63, closes #63) — snapshots now render in collapsible per-dataset sections whose header row shows the dataset name, a snapshot-count chip, total used space, and the newest snapshot's age; clicking the header (or its toggle) collapses the group. Rows show the label, used/refer, and a relative age ("3d ago", absolute date on hover) instead of a full timestamp; the dataset column is gone (the group header carries it). Selection got a per-group checkbox alongside the global select-all — both scoped to the current filter — feeding the same "Delete selected" toolbar. Row actions (Diff/Clone/Send/Delete) are hover/focus-revealed like the datasets tab. The dataset drawer's "View →" button now switches tabs, expands the dataset's group, and scrolls to it.

- **Datasets tab redesigned around a detail drawer** (part 1 of #63) — the table now groups datasets into per-pool sections whose header row carries the pool's health badge and a capacity bar; rows show a usage bar against quota, passive status chips (NFS/SMB/iSCSI/auto-snap/ACL), and quick actions that appear on hover or keyboard focus (always visible on touch). Clicking a row slides in a detail drawer with actions grouped by concern: Properties (schema-driven inline editing with inherited values shown greyed instead of blanked, single Apply, per-user quotas, zfs rewrite), Sharing (NFS/SMB or iSCSI), Permissions (ACL, chown), Snapshots (count, schedule, jump to Snapshots tab), and a red-bordered Danger zone (rename, delete). The old Edit Properties dialog is gone — the drawer supersedes it; rewrite moved to its own dialog. SSE updates refresh the drawer's read-only sections live but never clobber an in-progress property edit. `datasets.js` split into three modules (`datasets.js` render, `dataset-drawer.js`, `dataset-dialogs.js`). No API changes.

- **Frontend event wiring consolidated on event delegation** — a `delegate()` helper in `static/js/utils.js` attaches one listener per stable container and dispatches on `data-action` attributes. All tabs (pools, datasets, snapshots, users/groups, SMB users, Time Machine, jobs, replication, services) and the re-rendered dialog lists (ACL entries, per-user quotas, SSH keys) now use it, replacing both per-render `querySelectorAll().addEventListener()` loops and pools.js's inline `onclick=` handlers. All ten `window.*` handler globals are gone. No behavior change; unblocks the datasets/snapshots UI overhaul (#63). Closes #120.

- **Single-command writes moved from Ansible to an in-process Go ops layer** (`internal/ops`) — scrub start/cancel, device replace/offline/online, pool create/import/export/add/remove-device, per-user/group quotas, and snapshot create/destroy/batch-destroy/clone now execute their one `zpool`/`zfs` command directly (argv, no shell), eliminating the ~1 s Python startup per write and the duplicated Go-regex/Jinja-assert validation for these ops. The API contract is unchanged: identical `{tasks: [...]}` step shape, same `ansible.progress` SSE topic, same op-log dialog. 15 command-wrapper playbooks deleted; Ansible remains required for config-file and OS-resource writes only (SMB, users/groups, TLS, ACLs, dataset property playbooks, scrub schedules, service control). New Prometheus metrics `dumpstore_op_runs_total` / `dumpstore_op_duration_seconds`. Closes #115.

### Added

- **ZFS capability detection and UI gating** — dumpstore now probes what the installed OpenZFS release actually supports (`internal/zfs.Caps()`: `zfs rewrite` subcommand recognition, draid in the `zpool upgrade -v` feature list — probing rather than version comparison, since distros backport) and exposes the result as a `capabilities` object in `GET /api/schema`. The UI gates accordingly: the "Rewrite existing blocks" section in Edit Dataset is replaced by an explanatory note on hosts without `zfs rewrite` (needs OpenZFS ≥ 2.3), draid topology options in Create Pool are disabled with a tooltip when the draid feature is absent, and the Sysinfo tab gained a ZFS section showing the version plus capability badges. No behavior change on hosts that support everything. Closes #119.

- **Scope boundary: manage vs integrate** — the project's scope rule is now written down (CLAUDE.md "Scope boundary" section, README philosophy paragraph, wiki Architecture page): dumpstore *manages* ZFS (pools, datasets, snapshots, replication, encryption) and sharing (SMB/NFS/iSCSI/wsdd) plus the local users they need; it only *integrates* with identity (lldap), observability (Prometheus/OTEL), power (NUT), and notifications — read, display, link out, minimal in-tree surface. The FEATURES.md Planned table carries a per-row Scope verdict, and the open integrate-domain issues (#62 lldap, #52 NUT, #49 OTEL) were annotated and scope-trimmed accordingly. Closes #121.

- **Release build smoke test on every PR** — the release recipe now lives in a single `make release VERSION=…` target (all four linux/freebsd × amd64/arm64 cross-builds with release ldflags, packaged into `dist/`), used verbatim by `release.yml` on tags and by a new `release-smoke` job in `ci.yml` on every PR, which also asserts the native binary reports the injected version via `--version`. A broken release pipeline is now caught at PR time instead of at tag time (the v0.1.14 tag build broke on a Go-version drift nothing ever exercised). Closes #118.

- **VM-based integration test suite** — `tests/integration` (build tag `integration`, stdlib only) drives a deployed dumpstore instance over HTTP and asserts on real ZFS state: session auth, dataset lifecycle (create / property PATCH / rename / destroy), snapshots with `zfs diff` and clone, per-user quotas, a `zfs send | recv` pipeline followed through the jobs API, and a full pool lifecycle — mirror create, duplicate-name refusal, device offline/online, `zpool replace` with resilver, hot-spare add/remove, export, importable discovery, import — on three dedicated 1 GiB scratch disks the Lima VMs now provision (never on the `tank` data pool). Run locally with `make test-integration`; CI (`.github/workflows/integration-tests.yml`) boots the same Lima VM on a KVM-enabled runner nightly, on manual dispatch, and on PRs labeled `run-integration`. Closes #117.

### Fixed

- **Clearing a dataset quota no longer fails** — blanking the quota field sent `zfs inherit quota`, which ZFS rejects because quota is not an inheritable property ("use 'zfs set quota=none' to clear"), so reverting a quota always errored. `zfs_dataset_set.yml` now maps an empty quota to `zfs set quota=none`.
- **FreeBSD dev VM provisioning died silently at the config-write step** — Lima provision scripts run with the base-system PATH, so `python3` (installed under `/usr/local/bin` by pkg) was not found and `set -e` aborted before writing `dumpstore.conf` or creating the `tank` pool. The provision script now exports `/usr/local/bin:/usr/local/sbin` onto PATH first.
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
