# dumpstore

A lightweight NAS management UI written in Go — built for Linux and FreeBSD, designed to stay out of the way of a vanilla system.

## Why this exists

I run a [Kobol Helios64](https://wiki.kobol.io/helios64/intro/) as my home NAS — a five-bay ARM board that deserves better than the software ecosystem currently offers it. The existing storage UIs I tried were either too heavy, too opinionated about the underlying distribution, or simply unmaintained. None of them gave me a clean, no-nonsense window into my ZFS pools without pulling in a container runtime, a database, or a Node.js server alongside them.

What I wanted was simple: observe and manage my storage from a browser, on a machine that stays as close to a vanilla Linux or FreeBSD installation as possible. No agents, no daemons-within-daemons, no frameworks that outlive their welcome. Just a single compiled binary, some Ansible playbooks, and a handful of static files.

dumpstore started as exactly that — a thin read-only dashboard — and is growing deliberately from there. The roadmap includes everything a real NAS UI needs: SMB/NFS share management, local user and group administration, fine-grained permissions, and eventually ACL support. Each feature will follow the same philosophy: keep the host clean, keep the code auditable, and let the operating system do the heavy lifting.

If you run a Helios64, an old server, or any ZFS box where you care about what is actually installed on it, this might be the tool for you.

## Features

- **System info** — hostname, OS, kernel, CPU, uptime, load averages, process stats
- **Pool overview** — health badges, usage bars, fragmentation, deduplication ratio, vdev tree
- **I/O statistics** — live read/write IOPS and bandwidth per pool
- **Disk health** — S.M.A.R.T. data per drive (temperature, power-on hours, reallocated sectors, pending sectors, uncorrectable errors)
- **Dataset browser** — depth-indented collapsible tree, compression, quota, mountpoint
- **Dataset creation** — create filesystems and volumes with any combination of ZFS properties
- **Dataset editing** — update properties in place (set or inherit)
- **Dataset deletion** — destroy datasets and volumes with recursive option and confirm-by-typing dialog
- **Snapshot management** — list, create (recursive), and delete snapshots
- **Prometheus metrics** — `GET /metrics` exposes Go runtime and process stats

## Architecture

### High-level overview

```
┌─────────────────────────────────────────────────────────┐
│                  Browser  (vanilla JS SPA)              │
│  state object → render functions → fetch → api()        │
└────────────────────────────┬────────────────────────────┘
                             │ HTTP :8080
                             ▼
┌─────────────────────────────────────────────────────────┐
│                       main.go                           │
│  • flag: -addr  -dir  -debug                            │
│  • startup: checks ansible-playbook in PATH,            │
│             playbooks/ and static/ dirs exist           │
│  • requestLogger middleware (method/path/status/ms)     │
│  • GET /      → http.FileServer  (static/)              │
│  • /api/*     → api.Handler                             │
└────────────────────────────┬────────────────────────────┘
                             │
               ┌─────────────┴──────────────┐
               │                            │
          READ requests               WRITE requests
   pools, datasets, snapshots,    create / edit / destroy
   iostat, status, props,         datasets and snapshots
   sysinfo, SMART, metrics
               │                            │
               ▼                            ▼
  ┌────────────────────────┐   ┌────────────────────────────┐
  │   internal/zfs/zfs.go  │   │ internal/ansible/runner.go │
  │   internal/system/     │   │                            │
  │   internal/smart/      │   │  Run(playbook, extraVars)  │
  │                        │   │                            │
  │  ListPools()           │   │  exec: ansible-playbook    │
  │  ListDatasets()        │   │    -i inventory/localhost  │
  │  ListSnapshots()       │   │    --extra-vars '{...}'    │
  │  IOStats()             │   │  env: ANSIBLE_STDOUT_      │
  │  GetDatasetProps()     │   │    CALLBACK=json           │
  │  PoolStatuses()        │   │                            │
  │  Version()             │   │  parse JSON output         │
  │  system.Get()          │   │  → []TaskStep              │
  │  smart.Collect()       │   │                            │
  │                        │   │                            │
  │  exec: zpool / zfs /   │   │                            │
  │  smartctl / sysctl     │   │                            │
  │  (no Python startup)   │   │                            │
  └───────────┬────────────┘   └────────────┬───────────────┘
              │                             │
              ▼                             ▼
        ZFS kernel                  playbooks/*.yml
        subsystem                   ┌──────────────────────┐
                                    │  targets: localhost  │
                                    │  gather_facts: false │
                                    │  1. assert vars      │
                                    │  2. zfs/zpool cmd    │
                                    └──────────────────────┘
```

### Why the read/write split?

| Concern | Reads | Writes |
|---|---|---|
| **Mechanism** | `exec.Command(zpool/zfs/smartctl)` | `exec.Command(ansible-playbook)` |
| **Latency** | Fast — no Python startup | ~1-2 s — acceptable for mutations |
| **Output** | Parsed from tab-separated stdout | Parsed from structured JSON callback |
| **Audit trail** | None needed | Task names + changed/failed per step |
| **Idempotency** | N/A | Enforced by playbook assert tasks |

### Request flow for a write operation

```
Browser
  │  POST /api/snapshots  {"dataset":"tank/data","snapname":"bkp"}
  ▼
handlers.go: createSnapshot()
  │  validate input (no @;|&$` chars)
  │  build extraVars map
  ▼
runner.go: Run("zfs_snapshot_create.yml", vars)
  │  marshal vars → --extra-vars '{"dataset":"tank/data",...}'
  │  set ANSIBLE_STDOUT_CALLBACK=json
  ▼
ansible-playbook (subprocess)
  │  assert: dataset defined, no bad chars
  │  command: zfs snapshot tank/data@bkp
  ▼
runner.go: parse JSON stdout → PlaybookOutput → []TaskStep
  ▼
handlers.go: return 201 {"snapshot":"tank/data@bkp","tasks":[...]}
  ▼
Browser: showOpLog() renders task steps in modal
```

### Route map

```
GET  /api/sysinfo             → /proc/*, sysctl     (direct)
GET  /api/version             → zpool version       (direct)
GET  /api/pools               → zpool list          (direct)
GET  /api/poolstatus          → zpool status        (direct)
GET  /api/datasets            → zfs list            (direct)
GET  /api/dataset-props/{n}   → zfs get             (direct)
GET  /api/snapshots           → zfs list -t snap    (direct)
GET  /api/iostat              → zpool iostat        (direct)
GET  /api/smart               → smartctl            (direct)
GET  /metrics                 → Prometheus text     (direct)

POST   /api/datasets          → zfs_dataset_create.yml    (ansible)
PATCH  /api/datasets/{n}      → zfs_dataset_set.yml       (ansible)
DELETE /api/datasets/{n}      → zfs_dataset_destroy.yml   (ansible)
POST   /api/snapshots         → zfs_snapshot_create.yml   (ansible)
DELETE /api/snapshots/{n}     → zfs_snapshot_destroy.yml  (ansible)
```

## Requirements

| | Linux | FreeBSD |
|---|---|---|
| ZFS | `zfsutils-linux` or equivalent | built-in (`zfsutils` pkg for older releases) |
| Ansible | `ansible` package (Python 3) | `py311-ansible` or equivalent |
| Service manager | systemd | rc.d (via `daemon(8)`) |
| S.M.A.R.T. (optional) | `smartmontools` | `smartmontools` pkg |
| Build | Go 1.22+ | Go 1.22+ |

Go and Ansible are the only hard requirements. ZFS must be available on the target machine; the binary itself builds and runs on any platform.

## Versioning

Releases are tagged with semver (`v0.1.0`, `v0.2.0`, …). The version is injected at build time via ldflags from `git describe`:

```
v0.1.0               ← exact tag
v0.1.0-3-gabcdef     ← 3 commits after tag
v0.1.0-3-gabcdef-dirty ← uncommitted changes present
dev                  ← built outside git (no tags)
```

The version is exposed in:
- `./dumpstore -version` — prints and exits
- `GET /api/sysinfo` → `app_version` field
- `GET /metrics` → `dumpstore_build_info{version="..."}` label
- UI version bar (alongside the OpenZFS version)

## Build & Install

`make install` detects the OS automatically and registers the appropriate service.

```bash
# Tag a release (optional — omitting gives "dev" as version)
git tag v0.1.0

# Build and install
make build
sudo make install
```

The service will be available at `http://localhost:8080`.

### Linux (systemd)

The unit file is installed to `/etc/systemd/system/dumpstore.service`.

To change the listen address:
```bash
# Edit ExecStart in the unit file, then:
sudo systemctl daemon-reload && sudo systemctl restart dumpstore
```

### FreeBSD (rc.d)

The rc script is installed to `/usr/local/etc/rc.d/dumpstore`. The installer runs `sysrc dumpstore_enable=YES` automatically.

To customise address or install path, add to `/etc/rc.conf`:
```
dumpstore_enable="YES"
dumpstore_addr=":9090"
dumpstore_dir="/usr/local/lib/dumpstore"
```
Then `service dumpstore restart`.

## Run without installing

```bash
go build -o dumpstore .
sudo ./dumpstore -addr :8080 -dir .
```

`-dir` must point to the directory that contains `playbooks/` and `static/`. It defaults to the directory of the executable.

## Uninstall

```bash
sudo make uninstall
```

## Project layout

```
.
├── main.go                          # HTTP server, flag parsing, startup dependency checks
├── go.mod
├── internal/
│   ├── zfs/zfs.go                   # Direct zpool/zfs command execution (reads)
│   ├── ansible/runner.go            # Ansible playbook execution + JSON output parsing
│   ├── api/handlers.go              # REST API handlers + input validation
│   ├── system/system.go             # Host + process info (/proc, sysctl)
│   └── smart/smart.go              # S.M.A.R.T. data via smartctl
├── playbooks/
│   ├── inventory/localhost          # Local connection inventory
│   ├── zfs_dataset_create.yml       # Create filesystem or volume
│   ├── zfs_dataset_set.yml          # Update dataset properties (set / inherit)
│   ├── zfs_dataset_destroy.yml      # Destroy dataset or volume
│   ├── zfs_snapshot_create.yml      # Create snapshot
│   └── zfs_snapshot_destroy.yml     # Destroy snapshot
├── static/
│   ├── index.html                   # Single-page application shell + dialogs
│   ├── app.js                       # Vanilla JS frontend, no dependencies
│   └── style.css                    # Dark monospace theme
├── dumpstore.service                # systemd unit file (Linux)
├── dumpstore.rc                     # rc.d script (FreeBSD)
└── Makefile                         # OS-aware build / install / uninstall
```

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/sysinfo` | Host and process info |
| GET | `/api/version` | OpenZFS version string |
| GET | `/api/pools` | List all pools with usage stats |
| GET | `/api/poolstatus` | Detailed pool status with vdev tree |
| GET | `/api/datasets` | List all datasets and volumes |
| GET | `/api/dataset-props/{name}` | Editable properties for a dataset |
| GET | `/api/snapshots` | List all snapshots |
| GET | `/api/iostat` | Pool I/O statistics (1-second sample) |
| GET | `/api/smart` | S.M.A.R.T. health per disk |
| GET | `/metrics` | Prometheus text exposition |
| POST | `/api/datasets` | Create a dataset or volume |
| PATCH | `/api/datasets/{name}` | Update dataset properties |
| DELETE | `/api/datasets/{name}` | Destroy a dataset or volume |
| POST | `/api/snapshots` | Create a snapshot |
| DELETE | `/api/snapshots/{name}` | Destroy a snapshot |

### POST /api/datasets

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

Body is a JSON object with any subset of editable properties. An empty string value resets the property to inherited; a non-empty value sets it explicitly. Unknown properties are ignored.

```json
{
  "compression": "zstd",
  "quota": "",
  "readonly": "on"
}
```

Editable properties: `compression`, `quota`, `mountpoint`, `recordsize`, `atime`, `exec`, `sync`, `dedup`, `copies`, `xattr`, `readonly`.

### DELETE /api/datasets/{name}

Append `?recursive=true` to also destroy all child datasets and snapshots.

Pool roots (e.g. `tank`) cannot be deleted via this endpoint — use `zpool destroy`.

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
