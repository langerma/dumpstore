# Integration tests

End-to-end tests that drive a **deployed dumpstore instance** over HTTP and
verify real host state — ZFS pools/datasets, users and groups, Samba, ACLs,
iSCSI, TLS and service state — inside the Lima dev VM. No mocks: every write goes through the same
Ansible/jobs machinery production uses.

They are excluded from `go test ./...` by the `integration` build tag.

## Running locally

```sh
make vm-linux-start     # boot the Lima VM (first run: creates it + 3 scratch disks)
make vm-linux-deploy    # build + install dumpstore inside the VM
make test-integration   # run the suite from the host against http://localhost:8080
```

The suite logs in as `admin` / `admin` (the dev VM default) and uses
`limactl shell` for fixtures the API can't provide (writing files into
datasets, pre-cleaning stale state) and to verify results on the host
(`id`, `getfacl`, `stat`, `zfs holds`).

The Linux VM is provisioned with the share/ACL tooling the suite exercises
(`acl samba nfs-kernel-server targetcli-fb zfs-auto-snapshot`); the FreeBSD
VM gets `samba416` (iSCSI uses `ctld` from base). A dev VM created before
those were added needs a rebuild (or a manual `pkg install samba416` /
`apt-get install …`) — tests that depend on a
missing tool skip with a hint rather than fail:

```sh
make vm-linux-destroy && make vm-linux-start && make vm-linux-deploy
```

## What is covered

| Test | Exercises |
|------|-----------|
| `TestAuth` | session gate (401), failed login, session cookie login |
| `TestLegacyPathsGone` | #181 hard cut: pre-v1 paths (`GET /api/pools`, `GET /api/sysinfo`, `POST /api/datasets/rename`) → 404 for a logged-in client |
| `TestDatasetLifecycle` | dataset create, property PATCH, nested-name properties read through a percent-encoded path segment, rename, destroy |
| `TestSnapshotsAndDiff` | snapshot create/list, `zfs diff` between snapshots, clone, batch delete |
| `TestUserQuota` | `userquota@` set/clear + userspace report |
| `TestSendReceiveJob` | `zfs send \| recv` pipeline through the jobs manager, job polling, job removal |
| `TestPoolLifecycle` | pool create (mirror), duplicate-name refusal, device offline/online, `zpool replace` + resilver, spare add/remove, export, importable discovery, import |
| `TestReadEndpoints` | 200 + top-level shape of every read endpoint (sysinfo incl. `otel`, network, version, schema, smart, iostat, poolstatus, devices, dataset properties, whoami, auth/config, tls, acl-status, importable pools, scrub/auto-snapshot schedules, jobs, services, users, groups, smb/status, iscsi targets, replication tasks) + unauthenticated `/metrics` (families present; `http_requests_total` `path` label is the route pattern, e.g. `/api/v1/datasets/{name}`) |
| *OpenAPI conformance* (after all tests, in `TestMain`) | #181: every JSON response the suite received validates against the served spec's schema for its operation and status, and every 4xx status is documented for that operation; `DUMPSTORE_RECORD=<file>` also writes the recordings as NDJSON |
| `TestOpenAPISpec` / `TestSchemaValidatorRejects` | #181: `GET /api/v1/openapi.json` is OpenAPI 3.1 with every operation; the live response of every parameterless GET with a declared schema validates against it (types, nullability, required fields, no undocumented properties, enums, minimums); the validator itself rejects mismatches |
| `TestReadNegatives` | invalid dataset name 400, unknown job 404, SSE with no valid topic 400 |
| `TestSSEEvents` | `/api/v1/events` stream headers + cached `user.query` replay on connect |
| `TestOperations` | #184: two clients create snapshots concurrently while one watches `ansible.progress` — each operation's live steps carry its ID with contiguous `seq` and match its HTTP response; `GET /api/v1/operations/{id}` returns the same steps/result, `?after=1` the tail; duplicate ID → 409 (not executed), invalid → 400, no header → generated ID; unknown → 404 |
| `TestUserLifecycle` / `TestUserNegatives` | user create/modify/delete, SSH key add/list (fingerprint matches `ssh-keygen -lf` in the VM)/remove by percent-encoded fingerprint, unknown fingerprint 404, `id` in the VM; invalid names 400, system user 403, unknown user 404 |
| `TestSSHKeyForeignPrimaryGroup` | #197: SSH key add for a user whose primary group isn't named after the user; `authorized_keys` owned by `<user>:<primary group>` |
| `TestGroupLifecycle` | group create, members + rename, unknown member 400, delete; system group 403 |
| `TestSMBSurface` | init (409 gate when smb.conf is absent), usershare set/list/unset by share name (delete returns name + path; unknown share 404), Samba user add/remove, `[homes]`, Time Machine shares |
| `TestPosixACL` | acltype gate, POSIX ACE set/get/remove (+ `getfacl` check), 404 on re-delete — Linux only |
| `TestNFSv4ACL` | NFSv4 ACE add/list/remove in the nfs4-acl-tools form (+ `getfacl` check), 404 on re-delete, bad permission 400 — FreeBSD only (Linux ZFS cannot apply NFSv4 ACLs locally) |
| `TestChown` | mountpoint owner/group read + change, verified with `stat`; `mountpoint=legacy` → 400 for GET and PUT |
| `TestDatasetRefGuard` | #183 reference guard: destroy (incl. recursive parent), rename, and mountpoint change of a usershare'd dataset → 409 with conflict list; replication source/target and iSCSI zvol → 409; stale usershare listed with `path_missing`, removed, and not resurrected by a new dataset at the same path; unreadable `smb.conf`/`smb4.conf` → 500 fail-closed with nothing destroyed. Runs on Linux and FreeBSD |
| `TestISCSITarget` | zvol → iSCSI target create/list/delete by IQN (unknown IQN 404; targetcli on Linux, ctld on FreeBSD — `/etc/ctl.conf`, its `.pre-dumpstore` backup, and ctld state restored afterwards); CHAP-without-password, bad IQN, and quote-in-secret 400. ctld: write before init 409, init imports an old block-patched `ctl.conf`, three targets share one portal and survive `service ctld restart`, duplicate IQN/zvol 409, unknown target 404 |
| `TestReplicationTask` | task CRUD, invalid schedule/target 400, manual run → job → received snapshot → history record, hold released; a re-held replication snapshot is destroyed by a single `DELETE` |
| `TestAutoSnapshotProperties` | per-dataset auto-snapshot properties set/read/inherit, value validation |
| `TestScrubSchedule` | scrub schedule add/remove (`/etc/default/zfs` backed up and restored) |
| `TestAutosnapTakeoverRelease` | #152, Linux: zfs-auto-snapshot cron entry points disabled by takeover (twice), restored byte-for-byte by release — skipped unless the daemon owns auto-snapshots initially |
| `TestAutosnapTakeoverReleaseFreeBSD` | #197, FreeBSD only: zfs-periodic switches in `/etc/periodic.conf` and `/etc/periodic.conf.local` set to `NO` by takeover (twice), restored byte-for-byte by release; the three conf files and the record are backed up and restored |
| `TestServices` | Samba restart + enable, unknown service 404, bad action/name 400 |
| `TestTLSGencert` | self-signed cert generation, status, regeneration with another hostname (new CN), cert/key config; invalid input 400 |
| `TestChangePassword` | wrong current 401, change → session survives, only the new password logs in → change back |
| `TestRewrite` | `zfs rewrite` background job, volume 400 — skipped when OpenZFS lacks `zfs rewrite` |

Deliberately **not** exercised: `change-username` (invalidates every session;
a mid-test failure would strand the VM's credentials), ACME issue/renew (needs
a real domain), and service stop/disable.
Tests that rewrite host config (`dumpstore.conf`, `/etc/default/zfs`) back the
file up and restore it byte-for-byte, so the next deploy starts from the
provisioned state.

Pool tests run on the three dedicated 1 GiB scratch disks
(`/dev/vdc`–`/dev/vde`), never on the `tank` data pool. All fixtures are
prefixed `itest` and cleaned up defensively before *and* after each test, so
an aborted run cannot poison the next one.

## Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `DUMPSTORE_URL` | `http://localhost:8080` | Base URL of the instance under test |
| `DUMPSTORE_VM` | `dumpstore-linux` | Lima VM name used for fixture commands |
| `DUMPSTORE_USER` / `DUMPSTORE_PASS` | `admin` / `admin` | Login credentials |
| `DUMPSTORE_TEST_POOL` | `tank` | Existing pool for dataset/snapshot tests |
| `DUMPSTORE_TEST_DISKS` | `/dev/vdc,/dev/vdd,/dev/vde` | Three unused disks for pool tests (set empty to skip them) |

### Against the FreeBSD VM

```sh
make vm-freebsd-start && make vm-freebsd-deploy
DUMPSTORE_URL=http://localhost:8081 \
DUMPSTORE_VM=dumpstore-freebsd \
DUMPSTORE_TEST_DISKS=/dev/vtbd2,/dev/vtbd3,/dev/vtbd4 \
make test-integration
```

## CI

`.github/workflows/integration-tests.yml` runs the Linux VM suite nightly,
on manual dispatch, and on PRs labeled `run-integration`. FreeBSD stays a
local target — GitHub's macOS arm64 runners lack nested virtualization.
