# Architecture

## Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Browser  (vanilla JS SPA)                       │
│  state object → render functions → api() helper                     │
│                                                                     │
│  ┌─ boot ──────────────────────────────────────────────────────┐    │
│  │  loadAll() → parallel REST fetches (fast path first)        │    │
│  │  startSSE() → EventSource /api/events?topics=…              │    │
│  │    on message: state[key] = data; render()                  │    │
│  │    on close:   fallback to setInterval(loadAll, 30 000)     │    │
│  └─────────────────────────────────────────────────────────────┘    │
└──────────────────────────┬──────────────────────────────────────────┘
                           │ HTTP :8080  (REST + SSE)
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                          main.go                                    │
│  • flag: -addr  -dir  -debug                                        │
│  • startup: checks ansible-playbook in PATH,                        │
│             playbooks/ and static/ dirs exist                       │
│  • signal.NotifyContext → graceful shutdown on SIGTERM/SIGINT       │
│  • logging.RequestLogger middleware: reads X-Request-ID from proxy  │
│    (or generates one), stores in ctx, logs req_id on every line    │
│  • GET /      → http.FileServer  (static/)                          │
│  • /api/*     → api.Handler                                         │
└───────────────────┬─────────────────────────────────────────────────┘
                    │
      ┌─────────────┼───────────────────────────────┐
      │             │                               │
      │     ┌───────┴──────────────────┐            │
      │     │  internal/broker         │            │
      │     │                          │            │
      │     │  Broker — pub/sub core   │◄── StartPoller() goroutine
      │     │    Subscribe(topic)      │    polls ZFS + users/groups every 10 s
      │     │    Publish(topic, data)  │    publishes only on change
      │     │    Unsubscribe(topic,ch) │
      │     │                          │
      │     │  GET /api/events         │──► streams SSE to browsers
      │     └──────────────────────────┘
      │
      ├─── READ requests                    WRITE requests ───────────┐
      │  pools, datasets, snapshots,      create / edit / destroy     │
      │  iostat, status, props,           datasets, snapshots,        │
      │  sysinfo, SMART, metrics,         users, groups, ACLs,        │
      │  users, groups, ACLs,             SMB users/shares/config,    │
      │  SMB users/shares/homes,          dataset chown, scrub,       │
      │  iSCSI targets,                   iSCSI targets,              │
      │  Time Machine shares              SMB homes config,           │
      │                                   Time Machine shares         │
      │                                                               │
      ▼                                                               ▼
┌───────────────────────┐                        ┌────────────────────────────┐
│  internal/zfs/zfs.go  │                        │ internal/ansible/runner.go │
│  internal/system/     │                        │                            │
│  internal/smart/      │                        │  Run(playbook, extraVars)  │
│  internal/iscsi/      │                        │                            │
│  internal/smb/        │                        │                            │
│                       │                        │  exec: ansible-playbook    │
│  ListPools()          │                        │    -i inventory/localhost  │
│  ListDatasets()       │                        │    --extra-vars '{...}'    │
│  ListSnapshots()      │                        │  env: ANSIBLE_STDOUT_      │
│  IOStats()            │                        │    CALLBACK=ndjson         │
│  GetDatasetProps()    │                        │                            │
│  GetDatasetACL()      │                        │  parse ndjson output       │
│  GetMountpointOwner() │                        │  → []TaskStep              │
│  PoolStatuses()       │                        │  streams live via SSE      │
│  Version()            │                        │                            │
│  system.Get()         │                        │                            │
│  system.ListUsers()   │                        │                            │
│  system.ListGroups()  │                        │                            │
│  system.ListServices()│                        │                            │
│  smb.ParseSMBConfig() │                        │                            │
│  smart.Collect()      │                        │                            │
│  iscsi.ListTargets()  │                        │                            │
│                       │                        │                            │
│  exec: zpool / zfs /  │                        │                            │
│  smartctl / sysctl /  │                        │                            │
│  pdbedit / net /      │                        │                            │
│  targetcli / ctld     │                        │                            │
│  (no Python startup)  │                        │                            │
└──────────┬────────────┘                        └────────────┬───────────────┘
           │                                                  │
           ▼                                                  ▼
     ZFS kernel                                       playbooks/*.yml
     subsystem                                        ┌──────────────────────────────────┐
                                                      │  targets: localhost              │
                                                      │  gather_facts: false             │
                                                      │  1. assert vars                  │
                                                      │  2. mutating command             │
                                                      │                                  │
                                                      │  config-owning services only:    │
                                                      │  2a. create referenced dirs      │
                                                      │  2b. render full config (tpl)    │
                                                      │  2c. restart service             │
                                                      └──────────────────────────────────┘
```

## Service ownership model

When dumpstore manages a service it takes **full ownership** of that service's config file. The config is rendered from a Go template on every write — no block-patching, no `lineinfile`, no partial edits. If you manually edit a managed config file, dumpstore will overwrite it on the next write operation.

The rule is binary: **own it completely, or don't touch it at all.**

| Service | Owned? | Config file | Restart mechanism |
|---------|--------|-------------|-------------------|
| Samba | ✅ full | `/etc/samba/smb.conf` / `/usr/local/etc/smb4.conf` | `systemctl restart smbd` / `service samba_server restart` |
| NFS | ✅ via ZFS | ZFS `sharenfs` property (ZFS manages `/etc/exports`) | automatic on `zfs set` |
| iSCSI | ✅ via CLI | `targetcli saveconfig` / `/etc/ctl.conf` | `targetcli` / `service ctld restart` |
| TLS | ✅ full | `dumpstore.conf` cert fields | dumpstore reload |
| Users / Groups | — | OS is source of truth | n/a |
| ZFS datasets | — | ZFS kernel properties | n/a |

For config-owning services the write path is extended:

```
playbooks/smb_apply.yml (example)
  ┌──────────────────────────────────┐
  │  targets: localhost              │
  │  gather_facts: false             │
  │  1. assert vars                  │
  │  2. create referenced dirs       │
  │  3. render full config (template)│
  │  4. restart service              │
  └──────────────────────────────────┘
```

Sub-features (shares, home dirs, Time Machine targets) are gated behind an **init gate** — they are disabled in the UI until the service has been bootstrapped with `POST /api/smb/init` (or equivalent). This prevents partial config states where the config file exists but hasn't been initialised by dumpstore yet.

## Read/write split

All read operations call ZFS/system CLI tools directly via `exec.Command`. All write operations go through Ansible playbooks.

| Concern         | Reads                              | Writes                               |
|-----------------|------------------------------------|--------------------------------------|
| **Mechanism**   | `exec.Command(zpool/zfs/smartctl)` | `exec.Command(ansible-playbook)`     |
| **Latency**     | Fast — no Python startup           | ~1–2 s — acceptable for mutations    |
| **Output**      | Parsed from tab-separated stdout   | Parsed from ndjson callback output   |
| **Audit trail** | None needed                        | Task names + changed/failed per step |
| **Idempotency** | N/A                                | Enforced by playbook `assert` tasks  |

This split exists to avoid Ansible's Python startup overhead on every read. Do not change it without a good reason.

## Write operation request flow

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
  │  set ANSIBLE_STDOUT_CALLBACK=ndjson
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

## Frontend

The frontend is vanilla JS with no build step. All data lives in a single `state` object. Render functions are pure — they read from `state` and write `innerHTML`.

A lightweight reactive store (`storeSet`/`storeBatch`/`subscribe`) automatically dispatches render functions when their subscribed state keys change. Each render function registers the state keys it depends on via `subscribe(keys, fn)`. Writing state via `storeSet(key, value)` triggers only the affected renderers. `storeBatch(fn)` coalesces multiple key updates so each renderer fires at most once per batch.

On boot:
1. `loadAll()` fetches all fast endpoints in parallel, wrapped in `storeBatch()` — each render fires once
2. `loadSlowMetrics()` fires in parallel for `/api/iostat` (~1 s) and `/api/smart` (drive scans), updating the I/O and disk health sections when ready
3. `startSSE()` opens a persistent `EventSource` connection; on each message `storeSet(key, data)` auto-dispatches the subscribed renderers
4. If SSE drops, the client falls back to `setInterval(loadAll, 30_000)` and retries SSE after 5 s

UI-local state (`collapsedDatasets`, `selectedSnaps`, `hideSystemUsers`, `hideSystemGroups`) is mutated directly on `state` with explicit render calls — it is not managed by the store.

## Playbook conventions

All playbooks target `localhost` with `gather_facts: false`. Each playbook:

1. Declares required extra vars in a header comment
2. Has an `assert` task that validates all inputs before any mutation
3. Has stable task names (the runner looks them up by name for `RunAndGetStdout`)

## Security

- Input to Ansible extra-vars is validated for shell-special characters (`@;|&$\``) before the playbook call
- `static/` is served by `http.FileServer` — do not put secrets there
- The service runs as root (required for ZFS); do not expose it on a public interface without authentication in front of it
