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
      │  SMB users/shares, chown          dataset chown, scrub        │
      │                                                               │
      ▼                                                               ▼
┌───────────────────────┐                        ┌────────────────────────────┐
│  internal/zfs/zfs.go  │                        │ internal/ansible/runner.go │
│  internal/system/     │                        │                            │
│  internal/smart/      │                        │  Run(playbook, extraVars)  │
│                       │                        │                            │
│  ListPools()          │                        │  exec: ansible-playbook    │
│  ListDatasets()       │                        │    -i inventory/localhost  │
│  ListSnapshots()      │                        │    --extra-vars '{...}'    │
│  IOStats()            │                        │  env: ANSIBLE_STDOUT_      │
│  GetDatasetProps()    │                        │    CALLBACK=ndjson         │
│  GetDatasetACL()      │                        │                            │
│  GetMountpointOwner() │                        │  parse ndjson output       │
│  PoolStatuses()       │                        │  → []TaskStep              │
│  Version()            │                        │  streams live via SSE      │
│  system.Get()         │                        │                            │
│  system.ListUsers()   │                        │                            │
│  system.ListGroups()  │                        │                            │
│  smart.Collect()      │                        │                            │
│                       │                        │                            │
│  exec: zpool / zfs /  │                        │                            │
│  smartctl / sysctl /  │                        │                            │
│  pdbedit / net        │                        │                            │
│  (no Python startup)  │                        │                            │
└──────────┬────────────┘                        └────────────┬───────────────┘
           │                                                  │
           ▼                                                  ▼
     ZFS kernel                                       playbooks/*.yml
     subsystem                                        ┌──────────────────────┐
                                                      │  targets: localhost  │
                                                      │  gather_facts: false │
                                                      │  1. assert vars      │
                                                      │  2. mutating command │
                                                      └──────────────────────┘
```

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
