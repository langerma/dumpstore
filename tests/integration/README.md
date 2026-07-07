# Integration tests

End-to-end tests that drive a **deployed dumpstore instance** over HTTP and
verify real ZFS state changes — `zpool create`, resilver, `zfs diff`, jobs —
inside the Lima dev VM. No mocks: every write goes through the same
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
datasets, pre-cleaning stale state).

## What is covered

| Test | Exercises |
|------|-----------|
| `TestAuth` | session gate (401), failed login, session cookie login |
| `TestDatasetLifecycle` | dataset create, property PATCH, rename, destroy |
| `TestSnapshotsAndDiff` | snapshot create/list, `zfs diff` between snapshots, clone, batch delete |
| `TestUserQuota` | `userquota@` set/clear + userspace report |
| `TestSendReceiveJob` | `zfs send \| recv` pipeline through the jobs manager, job polling, job removal |
| `TestPoolLifecycle` | pool create (mirror), duplicate-name refusal, device offline/online, `zpool replace` + resilver, spare add/remove, export, importable discovery, import |

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
