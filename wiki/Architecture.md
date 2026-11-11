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

This is one half of the project's **scope boundary** (#121). The other half: domains dumpstore does *not* manage, it only **integrates** with — observability (Prometheus/OTEL: export only, no embedded dashboards or alerting) and notifications (syslog/webhook out). Identity (lldap) and power (NUT) would fall in this bucket too, but neither is pursued (#62 and #52 closed as won't fix): local users are the only identity source, and UPS shutdown stays with NUT itself. The test for every feature: does it make dumpstore better at running ZFS + shares, or does it rebuild a tool that already exists?

| Service | Owned? | Config file | Restart mechanism |
|---------|--------|-------------|-------------------|
| Samba | ✅ full | `/etc/samba/smb.conf` / `/usr/local/etc/smb4.conf` | reload `smbd`/`nmbd` / `samba_server` when running, start them when stopped |
| wsdd | ✅ full (Linux) | unit's EnvironmentFile (`/etc/default/wsdd` on Debian/Ubuntu), rendered from `smb.conf`; FreeBSD rc.d reads `smb4.conf` directly | `systemctl restart wsdd-server` (only if running and options changed) |
| NFS | ✅ via ZFS | ZFS `sharenfs` property (ZFS manages `/etc/exports`) | automatic on `zfs set` |
| iSCSI (FreeBSD) | ✅ full | `/etc/ctl.conf`, rendered from `internal/iscsi/ctl.conf.tmpl`; init gate `POST /api/v1/iscsi/init` | `service ctld reload` (start if stopped), verified via `ctladm portlist` + listening sockets, rolled back on failure |
| iSCSI (Linux) | ✅ via CLI | `targetcli saveconfig` | `targetcli` |
| TLS | ✅ full | `dumpstore.conf` cert fields; certificates under `<config dir>/tls` | restart dumpstore with `--tls` (the certificate is loaded once at startup) |
| Users / Groups | — | OS is source of truth | n/a |
| ZFS datasets | — | ZFS kernel properties | n/a |

**OS auto-snapshot daemons** are not owned either: *takeover* only switches them off, records exactly what it changed in dumpstore's state dir, and *release* puts back what it recorded. On Linux it disables `zfs-auto-snapshot` timers or renames its cron files; on FreeBSD it sets the `zfs-periodic` switches (`{hourly,daily,weekly,monthly}_zfs_snapshot_enable`) that are `YES` in any periodic.conf to `NO` with `sysrc -f`, no `lineinfile` (#197) zfstools runs from crontab lines the admin wrote; dumpstore never edits a crontab, so takeover refuses with `409` until those lines are removed (#208).

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

Sub-features are gated behind an **init gate**: writes return `409` and the UI offers an *Initialize* action until the service has been bootstrapped.

- **Samba** (`POST /api/v1/smb/init`) — the gate checks for dumpstore's header (`# This file is managed by dumpstore.`) on the first line of `smb.conf`, which every rendered file has carried since #82. A distro-default or hand-written `smb.conf` therefore isn't initialised: writes return `409` until *Initialize Samba* has backed it up to `smb.conf.pre-dumpstore` and rendered dumpstore's own, carrying over workgroup, server string, `[homes]` and Time Machine shares (#193). A file block-patched by a dumpstore older than #82 has no header and goes through the same init.
- **ctld** (`POST /api/v1/iscsi/init`, FreeBSD) — the gate checks for dumpstore's ownership header on the first line of `/etc/ctl.conf`, so a pre-existing file is never overwritten before init has backed it up and imported its targets.

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

Both write lanes return the same `tasks: [...]` step shape (`ansible.TaskStep`) and stream live progress on the `ansible.progress` SSE topic — the frontend op-log cannot tell them apart. Each write request is an *operation* (`internal/operations`, #184): the `Handler.Operations` middleware registers it under the client's `X-Operation-ID` (or a generated ID) and records the final status and body; `runOp`/`runLocal` record every step with a per-operation `seq` and publish it tagged with the ID. The frontend shows only its own operation's steps and fills gaps or recovers a lost response from `GET /api/v1/operations/{id}`. Records are in memory and bounded (running always kept; finished: 200 / 1 h). Input validation for ops-lane writes lives solely in the Go handlers. Ansible (and Python) remains a dependency only for the config-file / OS-resource features.

## Route registry and OpenAPI

Routes are not registered on the `ServeMux` directly. Each one goes through `internal/apispec.Registry.Handle` with its metadata: SDK group and method (emitted as Forge's `x-forge-group-name` / `x-forge-method-name` and Fern's `x-fern-sdk-*`, and used to derive `operationId` and the docs tag), summary, parameter, request and response types, success status, and whether it is public or internal (#181). `Registry.Spec()` builds the OpenAPI 3.1 document from exactly these registrations, reflecting JSON schemas from the Go types, so the spec cannot list a route the server doesn't serve or miss one it does.

Every JSON route uses the typed adapter `handle(reg, route(...), fn)` with `fn func(ctx, In) (Out, error)` (#181 Phase 2):
- **Binding.** The adapter binds `path:"x"` / `query:"x"` fields, which may come from embedded shared param structs, and one `body:""` field (`body:"optional"` accepts an empty body).
- **Responses.** It writes the route's success status (201/202, or 204 with no body) and the client address the audit log needs.
- **Errors:** `errStatus(code, err)` for 4xx; `playbookError` / `localError` for a failed write (500 with its op-log steps); `conflictError` for the reference guard's 409. Anything else is a 500.
- **Spec types.** The spec takes its parameter, request and response schemas from `In` and `Out`, so they can't disagree with the code.
- **Error statuses.** Each route lists its 4xx statuses with `fails(...)`.
- **Exceptions.** Only non-JSON or streaming routes (SSE, `/metrics`, the spec itself, the HTML login flow) are plain handlers; `TestNoUntypedRoutes` keeps it that way.

The committed `api/openapi.json` is regenerated with `make openapi`; `TestOpenAPISpec` fails while it is stale, `TestSpecPathsResolve` requests every documented path with percent-encoded names and checks it reaches the same mux pattern, and CI lints the file with Redocly (`redocly.yaml`, `recommended-strict`). The integration suite records every JSON response it receives. After the run it validates each one against the served spec's schema for that operation and status, and fails on any 4xx status the operation doesn't list. This covers writes and error responses too.

API paths live under `/api/v1` (#181 Phase 3). Every path parameter is one segment, so names are percent-encoded (`/datasets/tank%2Fdata/acl`); `TestSpecPathsResolve` requests each route with a `/`, `@` and space-containing value. The frontend builds all paths with the `v1` tagged template in `static/js/utils.js`, which encodes every interpolated value. `TestFrontendPaths` scans `static/` and fails on any path that doesn't resolve to a registered route with that method, or on a hand-written `/api/` path. HTTP metrics and span names use the matched route pattern; the Operations middleware hands the pattern back to the outer middleware.

CI proves the spec is SDK-ready (#181 Phase 4): the `forge-sdk` job builds [Forge](https://github.com/cloudflare/forge) from a pinned commit (it isn't on npm yet), generates the TypeScript SDK from `api/openapi.json`, checks the client has the API's groups, and type-checks it strictly. Generated SDKs percent-encode path parameters themselves, so SDK users pass raw names. Internal routes (SSE, `/metrics`, the HTML login page, the spec itself) carry `x-forge-skip` and `x-fern-ignore`, so SDKs and SDK docs leave them out.

## Write operation request flow

```
Browser
  │  POST /api/v1/snapshots  {"dataset":"tank/data","snapname":"bkp"}
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

Playbook values never travel on the command line. The runner writes them to a
temporary vars file (mode `0600`, removed after the run) and passes
`--extra-vars @<file>`. The file's root mapping is tagged `!unsafe`, so Ansible
treats every value as plain data and never evaluates `{{ … }}` or `{% … %}`
inside it (#214). Playbooks still use the values normally (`{{ var }}`,
`var == "true"`, `var | bool`, `var | from_json`).

## Frontend

The frontend is vanilla JS with no build step. All data lives in a single `state` object. Render functions are pure — they read from `state` and write `innerHTML`.

A lightweight reactive store (`storeSet`/`storeBatch`/`subscribe`) automatically dispatches render functions when their subscribed state keys change. Each render function registers the state keys it depends on via `subscribe(keys, fn)`. Writing state via `storeSet(key, value)` triggers only the affected renderers. `storeBatch(fn)` coalesces multiple key updates so each renderer fires at most once per batch.

On boot:
1. `loadAll()` fetches all fast endpoints in parallel, wrapped in `storeBatch()` — each render fires once
2. `loadSlowMetrics()` fires in parallel for `/api/v1/pools/iostat` (~1 s) and `/api/v1/system/smart` (drive scans), updating the I/O and disk health sections when ready
3. `startSSE()` opens a persistent `EventSource` connection; on each message `storeSet(key, data)` auto-dispatches the subscribed renderers
4. If SSE drops, the client falls back to `setInterval(loadAll, 30_000)` and retries SSE after 5 s

Writes run inside an op-log session: `const op = startOp(title)` opens the op-log, `op.api(...)` sends each write with a fresh `X-Operation-ID`, and `op.done(title, tasks, err)` renders the result. Live `ansible.progress` steps are appended only for the active session's IDs, in `seq` order; on SSE reconnect or a `seq` gap the missing steps are fetched from `GET /api/v1/operations/{id}?after=<seq>`, and a write whose response was lost is resolved by polling that endpoint.

UI-local state (`collapsedDatasets`, `selectedSnaps`, `hideSystemUsers`, `hideSystemGroups`) is mutated directly on `state` with explicit render calls — it is not managed by the store.

## Playbook conventions

All playbooks target `localhost` with `gather_facts: false`. Each playbook:

1. Declares required extra vars in a header comment
2. Has an `assert` task that validates all inputs before any mutation
3. Has stable task names (the runner looks them up by name for `RunAndGetStdout`)

## Observability

How to enable it, the metric list, and the log format are on the [[Observability]] page; this section covers the design.

Prometheus stays the pull path (`GET /metrics`, always on). OpenTelemetry is the push path, gated entirely by the standard `OTEL_EXPORTER_OTLP_*` env vars — unset means nothing is exported and tracing is a no-op.

- **Traces** — `otelhttp` wraps the middleware chain (outermost, so `RequestLogger` sees the span); the span is renamed to `METHOD /route/pattern` after mux dispatch and tagged with `req_id`. Requests that never reach a route (`r.Pattern` empty) are named `METHOD (unauthenticated)` for 401/403 or `METHOD (unmatched)` otherwise, with a `dumpstore.short_circuit` attribute — never the raw path, to keep span-name cardinality bounded. The Ansible runner and the ops layer open child spans from the request context; they use `context.WithoutCancel` so a client disconnect can never kill a mutating command. Jobs get root spans linked (not parented) to the dispatching request since they outlive it; scheduled replication runs and autosnap fires get root spans of their own. ZFS read calls are deliberately untraced.
- **Logs (single-producer pipeline)** — slog's default handler is an `otelslog` bridge into one `sdk/log.LoggerProvider`. Its synchronous journald exporter (`internal/logging.JournalExporter`) reproduces the pre-OTEL output byte-for-byte (golden-tested), including syslog `<N>` priority prefixes, and appends `trace_id`/`span_id` when present; the OTLP branch is a batch processor on the *same* provider, so both sinks emit the same record by construction. Never tee logs at the slog level. `-log-stdout=false` drops the journald branch for OTLP-only setups (startup warning when that would leave no sink at all).
- **Metrics (one registry)** — every dumpstore metric is recorded once on the OTEL meter API (`otel.Meter("dumpstore")` in `internal/api`, `internal/ansible`, `internal/ops`; Go runtime stats and build info via one observable callback). The meter provider always runs with a Prometheus exporter reader on a private registry, which serves `GET /metrics`; translation is pinned to `UnderscoreEscapingWithSuffixes` and `target_info`/`otel_scope_*` are suppressed, so the exposition matches the pre-OTEL names, types, labels and buckets (golden-tested in `metrics_test.go`). With OTLP enabled, an OTLP reader on the *same* provider pushes those instruments plus the OTEL Go runtime instrumentation and otelhttp's `http.server.*` (which then also appear on `/metrics`). `internal/api` can't import `internal/otel` (cycle via `internal/logging`), so `main` injects the `/metrics` handler.

## Project layout

| Path | Responsibility |
|------|----------------|
| `main.go` | Flags, startup dependency checks, middleware chain, wiring of scheduler/jobs/poller |
| `internal/api` | HTTP handlers and route registration (`routes.go`, typed adapter in `route.go`); input validation for all writes; dataset reference guard (`refguard.go`) |
| `internal/apispec` | Route registry and OpenAPI 3.1 generator (#181) |
| `api/openapi.json` | Generated, committed OpenAPI spec (`make openapi`) |
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

- Handlers validate every value with allow-list validators before it reaches an argv or a playbook
- Playbook values are passed as `!unsafe` data in a 0600 vars file, never on the command line, so Ansible does not template them (#214)
- `static/` is served by `http.FileServer` — do not put secrets there
- The service runs as root (required for ZFS); do not expose it on a public interface without authentication in front of it

## Testing

- **Unit tests** — `go test ./...`; parser- and validator-level, no external dependencies.
- **Integration tests** — `tests/integration` (build tag `integration`) drives a **deployed instance** in the Lima dev VM over HTTP and asserts on real host state: every read endpoint, auth and password change, dataset/snapshot lifecycle, `zfs diff`, quotas, send/recv and replication jobs, users/groups, Samba, ACLs, chown, iSCSI, TLS, service control, auto-snapshot properties, scrub schedules, and a full pool lifecycle (create, offline/online, `zpool replace` + resilver, spares, export/import) on three dedicated scratch disks. Tests skip when a host tool is missing (stale VM, FreeBSD). Run with `make vm-linux-start && make vm-linux-deploy && make test-integration`.
- **Dev VMs** — `make vm-linux-*` (Ubuntu 24.04, follows the host architecture) and `make vm-freebsd-*` (FreeBSD 15, arm64) run headless under [Lima](https://github.com/lima-vm/lima) via QEMU; targets are `start`, `deploy`, `ssh`, `stop`, `destroy`. Each VM gets a 10 GiB disk with a `tank` pool created at first boot plus three 1 GiB scratch disks for the integration suite. `deploy` packs the source tree, copies it in, and runs `make install` natively — no cross-compilation. Port forwards are fixed (Linux `:8080`, FreeBSD `:8081`) so both can run at once; login `admin` / `admin`. `make dev` instead runs the UI on any machine against the CLI stubs in `dev/bin/`.
- **Screenshots** — `make screenshots` seeds demo data on the deployed Linux VM (`dev/screenshots/seed.sh up`), drives headless Chrome through every tab and dialog (`dev/screenshots/capture.mjs`, `puppeteer-core` against the installed Chrome; `CHROME_PATH` overrides), writes `screenshots/`, and always removes the demo data again (`seed.sh down`).
- **CI** — `ci.yml` runs build/vet/unit tests (including the OpenAPI staleness check), an `openapi-lint` job (Redocly CLI on `api/openapi.json`), plus a `release-smoke` job (the full `make release` cross-build matrix with a `--version` check) on every PR; `integration-tests.yml` boots the Lima VM on a KVM-enabled runner and runs the integration suite nightly, on manual dispatch, and on PRs labeled `run-integration`.
