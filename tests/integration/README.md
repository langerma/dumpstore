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
(`acl samba nfs-kernel-server targetcli-fb zfs-auto-snapshot`). A dev VM
created before those were added needs a rebuild — tests that depend on a
missing tool skip with a hint rather than fail:

```sh
make vm-linux-destroy && make vm-linux-start && make vm-linux-deploy
```

## What is covered

| Test | Exercises |
|------|-----------|
| `TestAuth` | session gate (401), failed login, session cookie login |
| `TestDatasetLifecycle` | dataset create, property PATCH, rename, destroy |
| `TestSnapshotsAndDiff` | snapshot create/list, `zfs diff` between snapshots, clone, batch delete |
| `TestUserQuota` | `userquota@` set/clear + userspace report |
| `TestSendReceiveJob` | `zfs send \| recv` pipeline through the jobs manager, job polling, job removal |
| `TestPoolLifecycle` | pool create (mirror), duplicate-name refusal, device offline/online, `zpool replace` + resilver, spare add/remove, export, importable discovery, import |
| `TestReadEndpoints` | 200 + top-level shape of every read endpoint (sysinfo incl. `otel`, network, version, schema, smart, iostat, poolstatus, devices, dataset-props, whoami, auth/config, tls/status, acl-status, importable pools, scrub/auto-snapshot schedules, jobs, services, users, groups, smb/status, iscsi-targets, replication) + unauthenticated `/metrics` |
| `TestReadNegatives` | invalid dataset name 400, unknown job 404, SSE with no valid topic 400 |
| `TestSSEEvents` | `/api/events` stream headers + cached `user.query` replay on connect |
| `TestUserLifecycle` / `TestUserNegatives` | user create/modify/delete, SSH key add/list/remove, `id` in the VM; invalid names 400, system user 403, unknown user 404 |
| `TestGroupLifecycle` | group create, members + rename, unknown member 400, delete; system group 403 |
| `TestSMBSurface` | init (409 gate when smb.conf is absent), usershare set/list/unset, Samba user add/remove, `[homes]`, Time Machine shares |
| `TestPosixACL` | acltype gate, POSIX ACE set/get/remove (+ `getfacl` check), 404 on re-delete — Linux only |
| `TestNFSv4ACL` | NFSv4 ACE add/list/remove in the nfs4-acl-tools form (+ `getfacl` check), 404 on re-delete, bad permission 400 — FreeBSD only (Linux ZFS cannot apply NFSv4 ACLs locally) |
| `TestChown` | mountpoint owner/group read + change, verified with `stat` |
| `TestISCSITarget` | zvol → targetcli target create/list/delete; CHAP-without-password and bad IQN 400 — Linux only |
| `TestReplicationTask` | task CRUD, invalid schedule/target 400, manual run → job → received snapshot → history record, hold released |
| `TestAutoSnapshotProperties` | per-dataset auto-snapshot properties set/read/inherit, value validation |
| `TestScrubSchedule` | scrub schedule add/remove (`/etc/default/zfs` backed up and restored) |
| `TestServices` | Samba restart + enable, unknown service 404, bad action/name 400 |
| `TestTLSGencert` | self-signed cert generation, status, cert/key config; invalid input 400 |
| `TestChangePassword` | wrong current 401, change → session survives, only the new password logs in → change back |
| `TestRewrite` | `zfs rewrite` background job, volume 400 — skipped when OpenZFS lacks `zfs rewrite` |

Deliberately **not** exercised: `change-username` (invalidates every session;
a mid-test failure would strand the VM's credentials), ACME issue/renew (needs
a real domain), service stop/disable, and auto-snapshot takeover/release (#152).
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
