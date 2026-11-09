# Architecture

## Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Browser  (vanilla JS, no build step)            │
│  state + reactive store: subscribe(keys, renderFn) / storeSet()     │
│  boot: loadAll() parallel REST fetches → startSSE() EventSource     │
│        SSE drops → poll loadAll() every 30 s, retry SSE after 5 s   │
└──────────────────────────┬──────────────────────────────────────────┘
                           │ HTTP(S) :8080  (REST + SSE)
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│  main.go                                                            │
│  • startup: zfs, zpool, ansible-playbook in PATH; playbooks/ and    │
│    static/ exist — otherwise refuse to start                        │
│  • middleware: otelhttp → RequestLogger (req_id, metrics) → auth    │
│  • /         → http.FileServer (static/)                            │
│  • /api/*    → internal/api handlers                                │
│  • scheduler (auto-snapshot, replication), jobs manager, poller     │
└───────┬──────────────────────────┬──────────────────────────┬───────┘
        │                          │                          │
        │ GET                      │ writes                   │ SSE
        ▼                          ▼                          ▼
┌───────────────┐ ┌────────────────────────────────┐ ┌────────────────┐
│ Reads         │ │ Writes — three lanes           │ │ internal/broker│
│ internal/zfs  │ │                                │ │ pub/sub + SSE  │
│ system, smart │ │ internal/ops   single argv cmd │ │                │
│ iscsi, smb    │ │   zpool/zfs, no shell, ~ms     │ │ poller: 10 s,  │
│ blockdev      │ │ internal/ansible  playbooks    │ │ publish only   │
│               │ │   config files, users, ACLs    │ │ on change      │
│ exec zpool/   │ │ internal/jobs  background      │ │                │
│ zfs/smartctl  │ │   zfs send|recv, zfs rewrite   │ │ writes stream  │
│ no Python     │ │                                │ │ op-log steps   │
└───────────────┘ └────────────────────────────────┘ └────────────────┘
```

## Service ownership model

When dumpstore manages a service it takes **full ownership** of that service's config file. The config is rendered from a Go template on every write — no block-patching, no `lineinfile`, no partial edits. If you manually edit a managed config file, dumpstore will overwrite it on the next write operation.

The rule is binary: **own it completely, or don't touch it at all.**

This is one half of the project's **scope boundary** (#121). The other half: domains dumpstore does *not* manage, it only **integrates** with — identity (lldap: auth bind + read-only display), observability (Prometheus/OTEL: export only, no embedded dashboards or alerting), power (NUT: status + thin shutdown hook), notifications (syslog/webhook out). The test for every feature: does it make dumpstore better at running ZFS + shares, or does it rebuild a tool that already exists?

| Service | Owned? | Config file | Restart mechanism |
|---------|--------|-------------|-------------------|
| Samba | ✅ full | `/etc/samba/smb.conf` / `/usr/local/etc/smb4.conf` | reload `smbd`/`nmbd` / `samba_server` (started if not running) |
| wsdd | ✅ full (Linux) | unit's EnvironmentFile (`/etc/default/wsdd` on Debian/Ubuntu), rendered from `smb.conf`; FreeBSD rc.d reads `smb4.conf` directly | `systemctl restart wsdd-server` (only if running and options changed) |
| NFS | ✅ via ZFS | ZFS `sharenfs` property (ZFS manages `/etc/exports`) | automatic on `zfs set` |
| iSCSI (FreeBSD) | ✅ full | `/etc/ctl.conf`, rendered from `internal/iscsi/ctl.conf.tmpl`; init gate `POST /api/iscsi/init` | `service ctld reload` (start if stopped), verified via `ctladm portlist` + listening sockets, rolled back on failure |
| iSCSI (Linux) | ✅ via CLI | `targetcli saveconfig` | `targetcli` |
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
  │  4. reload service               │
  └──────────────────────────────────┘
```

Sub-features (shares, home dirs, Time Machine targets) are gated behind an **init gate** — they are disabled in the UI until the service has been bootstrapped with `POST /api/smb/init` (or equivalent). This prevents partial config states where the config file exists but hasn't been initialised by dumpstore yet.

### Reference guard

Owned config points at datasets — shares and Time Machine targets by path, iSCSI targets and replication tasks by dataset name. Destroying, renaming, or re-mounting a dataset underneath them would leave config that fails at runtime, or — for Samba usershares — silently re-activates on whatever dataset next appears at that path. `internal/api/refguard.go` therefore checks every such reference before a destroy, rename, or mountpoint change and refuses with `409` plus a structured `conflicts` list (#183). It reads the usershare files directly (`internal/smb.ListUsershares`), since `net usershare info` hides shares whose path is gone. The check fails closed on any read error and runs under `Handler.refMu`, which every handler that *adds* a reference also holds (taken before `smbMu`), so a reference can't appear between check and mutation.

## Execution lanes

Reads call ZFS/system CLI tools directly. Writes are split across three lanes by shape (#115):

| Lane | Mechanism | Used for |
|------|-----------|----------|
| **Reads** | `exec.Command(zpool/zfs/smartctl)` | Everything `GET` — fast, no Python startup |
| **Single-command writes** | `internal/ops` — in-process argv exec, no shell | scrub, replace, offline/online, pool create/import/export/add/remove, quotas, snapshot create/destroy/clone |
| **Config-file / OS-resource writes** | `exec.Command(ansible-playbook)` + ndjson callback | smb.conf rendering, users/groups, TLS, ACLs, dataset property playbooks, scrub schedules, service control |
| **Long-running data-plane** | `internal/jobs` — background process groups | `zfs send \| recv`, `zfs rewrite` |

Both write lanes return the same `tasks: [...]` step shape (`ansible.TaskStep`) and stream live progress on the `ansible.progress` SSE topic — the frontend op-log cannot tell them apart. Each write request is an *operation* (`internal/operations`, #184): the `Handler.Operations` middleware registers it under the client's `X-Operation-ID` (or a generated ID) and records the final status and body; `runOp`/`runLocal` record every step with a per-operation `seq` and publish it tagged with the ID. The frontend shows only its own operation's steps and fills gaps or recovers a lost response from `GET /api/operations/{id}`. Records are in memory and bounded (running always kept; finished: 200 / 1 h). Input validation for ops-lane writes lives solely in the Go handlers. Ansible (and Python) remains a dependency only for the config-file / OS-resource features.

## Write operation request flow

```
Browser
  │  POST /api/snapshots  {"dataset":"tank/data","snapname":"bkp"}
  │  X-Operation-ID: 3f9c0a…   (startOp() session, one ID per request)
  ▼
zfs_handlers.go: createSnapshot()
  │  validate input (names, labels — Go is the single validation point)
  │  build argv: ["zfs", "snapshot", "tank/data@bkp"]
  ▼
internal/ops: Run(steps)          ← in-process, no shell, no Python
  │  exec zfs snapshot tank/data@bkp
  │  step result → ansible.TaskStep{name, status, msg}
  │  each step → operations registry: seq++, record,
  │  publish {op, seq, …} on ansible.progress (live op-log)
  ▼
handlers.go: return 201 {"snapshot":"tank/data@bkp","tasks":[...]}
  │  Operations middleware records status + body under the op ID
  ▼
Browser: op.done() renders task steps in modal

Config-file writes (e.g. SMB shares, user creation) take the same shape
but execute via ansible-playbook with the ndjson callback.
```

## Frontend

The frontend is vanilla JS with no build step. All data lives in a single `state` object. Render functions are pure — they read from `state` and write `innerHTML`.

A lightweight reactive store (`storeSet`/`storeBatch`/`subscribe`) automatically dispatches render functions when their subscribed state keys change. Each render function registers the state keys it depends on via `subscribe(keys, fn)`. Writing state via `storeSet(key, value)` triggers only the affected renderers. `storeBatch(fn)` coalesces multiple key updates so each renderer fires at most once per batch.

On boot:
1. `loadAll()` fetches all fast endpoints in parallel, wrapped in `storeBatch()` — each render fires once
2. `loadSlowMetrics()` fires in parallel for `/api/iostat` (~1 s) and `/api/smart` (drive scans), updating the I/O and disk health sections when ready
3. `startSSE()` opens a persistent `EventSource` connection; on each message `storeSet(key, data)` auto-dispatches the subscribed renderers
4. If SSE drops, the client falls back to `setInterval(loadAll, 30_000)` and retries SSE after 5 s

Writes run inside an op-log session: `const op = startOp(title)` opens the op-log, `op.api(...)` sends each write with a fresh `X-Operation-ID`, and `op.done(title, tasks, err)` renders the result. Live `ansible.progress` steps are appended only for the active session's IDs, in `seq` order; on SSE reconnect or a `seq` gap the missing steps are fetched from `GET /api/operations/{id}?after=<seq>`, and a write whose response was lost is resolved by polling that endpoint.

UI-local state (`collapsedDatasets`, `selectedSnaps`, `hideSystemUsers`, `hideSystemGroups`) is mutated directly on `state` with explicit render calls — it is not managed by the store.

## Playbook conventions

All playbooks target `localhost` with `gather_facts: false`. Each playbook:

1. Declares required extra vars in a header comment
2. Has an `assert` task that validates all inputs before any mutation
3. Has stable task names (the runner looks them up by name for `RunAndGetStdout`)

## Observability

How to enable it is in [[Installation]] → Observability; this section covers the design.

Prometheus stays the pull path (`GET /metrics`, always on). OpenTelemetry is the push path, gated entirely by the standard `OTEL_EXPORTER_OTLP_*` env vars — unset means every OTEL call site is a no-op.

- **Traces** — `otelhttp` wraps the middleware chain (outermost, so `RequestLogger` sees the span); the span is renamed to `METHOD /route/pattern` after mux dispatch and tagged with `req_id`. Requests that never reach a route (`r.Pattern` empty) are named `METHOD (unauthenticated)` for 401/403 or `METHOD (unmatched)` otherwise, with a `dumpstore.short_circuit` attribute — never the raw path, to keep span-name cardinality bounded. The Ansible runner and the ops layer open child spans from the request context; they use `context.WithoutCancel` so a client disconnect can never kill a mutating command. Jobs get root spans linked (not parented) to the dispatching request since they outlive it; scheduled replication runs and autosnap fires get root spans of their own. ZFS read calls are deliberately untraced.
- **Logs (single-producer pipeline)** — slog's default handler is an `otelslog` bridge into one `sdk/log.LoggerProvider`. Its synchronous journald exporter (`internal/logging.JournalExporter`) reproduces the pre-OTEL output byte-for-byte (golden-tested), including syslog `<N>` priority prefixes, and appends `trace_id`/`span_id` when present; the OTLP branch is a batch processor on the *same* provider, so both sinks emit the same record by construction. Never tee logs at the slog level. `-log-stdout=false` drops the journald branch for OTLP-only setups (startup warning when that would leave no sink at all).
- **Metrics** — Go runtime metrics and otelhttp's `http.server.*` pushed via OTLP; the Prometheus registry is untouched (migrating it to the OTEL meter, #168, is deliberately deferred).

## Project layout

| Path | Responsibility |
|------|----------------|
| `main.go` | Flags, startup dependency checks, middleware chain, wiring of scheduler/jobs/poller |
| `internal/api` | HTTP handlers and route registration; input validation for all writes; dataset reference guard (`refguard.go`) |
| `internal/zfs` | Direct `zpool`/`zfs` reads; POSIX/NFSv4 ACL parsing |
| `internal/ops` | Single-command writes (argv exec, no shell) |
| `internal/ansible` | Playbook runner (ndjson callback) and its metrics |
| `internal/jobs` | Background process groups for long-running work, persisted status |
| `internal/scheduler` | 5-field cron parser and ticker |
| `internal/replication` | Scheduled send/recv tasks: holds, incrementals, pruning |
| `internal/autosnap` | Native `com.sun:auto-snapshot*` execution and daemon takeover |
| `internal/smb` | Owned `smb.conf` and wsdd options: parse and render from templates; usershare file reader |
| `internal/iscsi` | Read iSCSI targets (targetcli saveconfig); FreeBSD: parse, import, and render the dumpstore-owned `ctl.conf` |
| `internal/system` | Host info, users/groups, services (systemd / rc.d), installed software |
| `internal/smart`, `internal/blockdev` | S.M.A.R.T. data; physical devices and vdev matching |
| `internal/broker` | SSE pub/sub and the background poller |
| `internal/operations` | Write-operation registry: IDs, seq-numbered steps, final result, bounded in-memory retention (#184) |
| `internal/auth` | Config file, sessions, login, rate limiting, trusted-proxy identity |
| `internal/logging`, `internal/otel` | slog pipeline (journald exporter), request logging, OTEL setup |
| `internal/schema`, `internal/platform` | Dataset property metadata; OS-specific paths |
| `playbooks/` | Ansible playbooks for config-file / OS-resource writes |
| `static/` | Frontend: `index.html`, `app.js`, per-tab modules in `static/js/`, `style.css` |
| `contrib/` | systemd unit and rc.d script |
| `tests/integration/` | End-to-end suite against a deployed VM |
| `dev/` | CLI stubs for `make dev` |

## Security

- Input to Ansible extra-vars is validated for shell-special characters (`@;|&$\``) before the playbook call
- `static/` is served by `http.FileServer` — do not put secrets there
- The service runs as root (required for ZFS); do not expose it on a public interface without authentication in front of it

## Testing

- **Unit tests** — `go test ./...`; parser- and validator-level, no external dependencies.
- **Integration tests** — `tests/integration` (build tag `integration`) drives a **deployed instance** in the Lima dev VM over HTTP and asserts on real host state: every read endpoint, auth and password change, dataset/snapshot lifecycle, `zfs diff`, quotas, send/recv and replication jobs, users/groups, Samba, ACLs, chown, iSCSI, TLS, service control, auto-snapshot properties, scrub schedules, and a full pool lifecycle (create, offline/online, `zpool replace` + resilver, spares, export/import) on three dedicated scratch disks. Tests skip when a host tool is missing (stale VM, FreeBSD). Run with `make vm-linux-start && make vm-linux-deploy && make test-integration`.
- **Dev VMs** — `make vm-linux-*` (Ubuntu 24.04, follows the host architecture) and `make vm-freebsd-*` (FreeBSD 15, arm64) run headless under [Lima](https://github.com/lima-vm/lima) via QEMU; targets are `start`, `deploy`, `ssh`, `stop`, `destroy`. Each VM gets a 10 GiB disk with a `tank` pool created at first boot plus three 1 GiB scratch disks for the integration suite. `deploy` packs the source tree, copies it in, and runs `make install` natively — no cross-compilation. Port forwards are fixed (Linux `:8080`, FreeBSD `:8081`) so both can run at once; login `admin` / `admin`. `make dev` instead runs the UI on any machine against the CLI stubs in `dev/bin/`.
- **Screenshots** — `make screenshots` seeds demo data on the deployed Linux VM (`dev/screenshots/seed.sh up`), drives headless Chrome through every tab and dialog (`dev/screenshots/capture.mjs`, `puppeteer-core` against the installed Chrome; `CHROME_PATH` overrides), writes `screenshots/`, and always removes the demo data again (`seed.sh down`).
- **CI** — `ci.yml` runs build/vet/unit tests plus a `release-smoke` job (the full `make release` cross-build matrix with a `--version` check) on every PR; `integration-tests.yml` boots the Lima VM on a KVM-enabled runner and runs the integration suite nightly, on manual dispatch, and on PRs labeled `run-integration`.
