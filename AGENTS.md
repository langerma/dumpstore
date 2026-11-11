# dumpstore — agent instructions

This file is the single source of truth for every coding agent working on dumpstore (Claude Code, OpenCode, Codex, …). `CLAUDE.md` only imports it (`@AGENTS.md`) — **edit this file, never a copy**. Where a rule mentions plan mode or subagents, use your tool's equivalent.

## Core Principles

- **Simplicity First**: Make every change as simple as possible. Impact minimal code.
- **No Laziness**: Find root causes. No temporary fixes. Senior developer standards.
- **Minimal Impact**: Changes should only touch what's necessary. Avoid introducing bugs.
- **Full ownership or hands off**: If dumpstore manages a service, it owns the config completely — rendered from a template, no block-patching. If it doesn't manage a service, it never touches that service's config. No half-ownership.

---

## Scope boundary — manage vs integrate

dumpstore is a thin, auditable layer over the OS, not a NAS distribution. Every feature request gets sorted into one of two buckets **before** design starts:

**Manage** (full ownership, config + lifecycle, in scope):

- ZFS: pools, datasets, snapshots, replication, encryption
- Sharing: SMB, NFS, iSCSI, and their discovery helpers (wsdd)
- The local users/groups/permissions those shares need

**Integrate** (read, display, link out — minimal in-tree surface):

- Identity (lldap): auth bind + read-only directory display; user/group management stays in lldap's own UI — currently not pursued (#62 closed, won't fix)
- Observability (Prometheus/Grafana/OTEL): export metrics/traces/logs; never embed dashboards, log viewers, or alerting pipelines
- Power (NUT): status display + a thin shutdown hook; no UPS configuration management — currently not pursued (#52 closed, won't fix)
- Notifications: emit to syslog/webhook; no in-tree notification subsystem

The test: *does the feature make dumpstore better at running ZFS + shares, or does it rebuild a tool that already exists?* If a mature tool owns the domain, dumpstore integrates with it and links out. Feature requests that only make sense in the "manage" bucket for an "integrate" domain are declined with a pointer to this section (see issue #121).

---

## Workflow & Process

### Plan before acting
- Enter plan mode for ANY non-trivial task (3+ steps or architectural decisions).
- Write detailed specs upfront to reduce ambiguity.
- If something goes sideways, STOP and re-plan — don't keep pushing.
- Use plan mode for verification steps, not just building.

### Subagent strategy
- Use subagents liberally to keep the main context window clean.
- Offload research, exploration, and parallel analysis to subagents.
- For complex problems, throw more compute at it via subagents.
- One task per subagent for focused execution.

### Verification before done
- Never mark a task complete without proving it works.
- Diff behavior between main and your changes when relevant.
- Ask yourself: "Would a staff engineer approve this?"
- Run tests, check logs, demonstrate correctness.

### Demand elegance (balanced)
- For non-trivial changes: pause and ask "is there a more elegant way?"
- If a fix feels hacky: "Knowing everything I know now, implement the elegant solution."
- Skip this for simple, obvious fixes — don't over-engineer.

### Autonomous bug fixing
- When given a bug report: just fix it. Don't ask for hand-holding.
- Point at logs, errors, failing tests — then resolve them.
- Go fix failing CI tests without being told how.

### Self-improvement loop
- After ANY correction from the user: update `rules/lessons.md` with the pattern.
- Write rules for yourself that prevent the same mistake.
- Review lessons at session start for relevant context.

### Docs & branch hygiene
- **Always create a feature branch** before implementing anything — `git checkout -b feat/<name>`.
- **Keep README.md, docs/index.html, and wiki/ up to date** whenever routes, features, architecture, or project layout change.

---

## Build & Check

**Run `make ci` before every commit and push** — it runs every PR check from `.github/workflows/` (the workflows call the same Makefile targets, so the two cannot drift) and prints a pass/fail table; logs in `.cache/ci/`. Backend changes also need `make ci-full` (the Linux **and** FreeBSD VM suites; CI runs them nightly and on every PR that touches backend paths, see `dev/ci/integration-needed.sh`). Don't push to find out whether CI passes.

```bash
make ci                    # all PR checks in parallel: ci-go ci-frontend ci-openapi-lint ci-forge ci-release ci-docs
make ci-full               # + Linux and FreeBSD VM suites in parallel, both with DUMPSTORE_REQUIRE_TOOLS=1
make ci-integration        # Linux VM suite only (what CI's linux-vm job runs)
make ci-freebsd            # FreeBSD VM suite only (what CI's freebsd-vm job runs)
go build ./...             # must always pass before committing
go vet ./...               # must always pass
go test ./...              # unit tests (no external dependencies)
go test -run TestFoo ./internal/api/   # run a single test by name
go test -tags integration ./tests/integration/...  # integration tests (requires live VM)
```

External Go dependencies:
- `golang.org/x/crypto/argon2` — password hashing for authentication (argon2id, PHC string format); official golang.org/x package
- `golang.org/x/term` — echo-disabled password prompting in `--set-password`; official golang.org/x package
- `go.opentelemetry.io/otel` + SDK/contrib modules — traces, metrics, and the log pipeline (#49); CNCF-governed. All OTLP exporters are env-gated (`OTEL_EXPORTER_OTLP_*`) and no-op by default. The log SDK always runs: `internal/logging.JournalExporter` is the single producer for journald output, and the OTLP log branch attaches to the same provider when enabled — never tee logs at the slog level. The meter provider always runs too (#168): record every metric once on `otel.Meter("dumpstore")`; never write Prometheus text by hand.
- No Go dependency for OpenAPI (#181): `internal/apispec` is stdlib reflection. The spec linter (`@redocly/cli`, pinned in the Makefile as `REDOCLY_VERSION`, config `redocly.yaml` + `.redocly.lint-ignore.yaml`) is CI-only Node tooling, never a runtime dependency.
- Forge (cloudflare/forge) is CI-only too: the `forge-sdk` job (`make ci-forge`) builds it from the commit pinned in `FORGE_REV` in the Makefile and type-checks the generated TypeScript SDK (#181). Routes with `Internal: true` are emitted with `x-forge-skip`/`x-fern-ignore` and get no SDK method. If you bump `FORGE_REV`, regenerate the SDK locally and review the output first.
- `go.opentelemetry.io/otel/exporters/prometheus` + `github.com/prometheus/client_golang` (`prometheus`, `promhttp`) + `github.com/prometheus/otlptranslator` — the meter's Prometheus reader that serves `/metrics` from a private registry (#168); CNCF projects. New metrics keep Prometheus-compatible names: counters without `_total` (the exporter adds it), durations as unit `s` (the exporter adds `_seconds`).

---

## Local development, install & release

```bash
make dev                  # run the UI on any machine (macOS too) against the CLI stubs in dev/bin/ (zfs, zpool, ansible-playbook)
make vm-linux-start       # Lima VM: Ubuntu 24.04 + ZFS + Ansible (UI on :8080); first run creates it
make vm-linux-deploy      # pack the tree, copy it in, `make install` inside the VM
make vm-linux-ssh         # shell in the VM
make test-integration     # drive the deployed instance over HTTP (see tests/integration/README.md)
make vm-freebsd-start     # FreeBSD 15 VM (UI on :8081); then make vm-freebsd-deploy
make vm-linux-verify      # fail if VM provisioning is incomplete (tools, tank pool); FreeBSD: vm-freebsd-verify
make openapi              # regenerate api/openapi.json after route/type changes
make screenshots          # regenerate screenshots/ against the Linux VM
sudo make install         # build + install binary, playbooks, static/ and the service (systemd or rc.d)
sudo make uninstall       # remove the service and /usr/local/lib/dumpstore (config and state dirs stay)
make release VERSION=v1.2.3   # cross-built release archives
```

Integration-test env vars (FreeBSD VM, disks, URL) are documented in `tests/integration/README.md`.

---

## Architecture

### Service ownership principle

**If dumpstore manages a service → full ownership, no exceptions.**
**If dumpstore does not manage a service → hands completely off.**

When dumpstore manages a service it:
- Owns the config file entirely — rendered from a template on every write, no block-patching
- Creates all referenced directories and prerequisite state as part of apply
- Reloads the service via the OS mechanism (systemd/rc.d) after every config change, starting it if it is stopped (wsdd: restarted only when running and its options changed)
- Exposes an init gate — sub-features are disabled until the service is bootstrapped

The service lifecycle (unit file, enable/disable) stays with the OS. dumpstore owns the config, not the service unit.

**Current ownership map:**

| Service | Owns config? | Config file |
|---------|-------------|-------------|
| Samba | ✅ full | `/etc/samba/smb.conf` / `/usr/local/etc/smb4.conf` |
| wsdd | ✅ full (Linux) | unit's EnvironmentFile (`/etc/default/wsdd`), rendered from smb.conf with Samba; FreeBSD: none (rc.d reads `smb4.conf`) |
| NFS | ✅ via ZFS | ZFS `sharenfs` property (ZFS manages `/etc/exports`) |
| iSCSI (FreeBSD) | ✅ full | `/etc/ctl.conf` — rendered from `internal/iscsi/ctl.conf.tmpl`, init gate `POST /api/v1/iscsi/init` (backup `ctl.conf.pre-dumpstore`) |
| iSCSI (Linux) | ✅ via CLI | `targetcli saveconfig` |
| TLS | ✅ full | `dumpstore.conf` |
| Users/Groups | — | OS is source of truth (`useradd`/`groupadd`) |

Do not add partial-ownership patterns (block-patching, lineinfile into a shared config). Either own the file fully or don't touch it.

### Read / write split

**Read ops → `internal/zfs` package (direct exec)**
`zpool`/`zfs` CLI commands are called directly via `exec.Command` for low latency. No Ansible for reads.

**Single-command writes → `internal/ops` (direct exec)**
Mutations that are just "assert inputs, run one argv command" — `zpool scrub/replace/offline/online/add/remove/create/import/export`, `zfs snapshot/destroy/clone`, `zfs set userquota@` — run in-process via `internal/ops`. Argv-based, no shell, no Python startup. Input validation lives in the Go handlers (single source; the old playbook asserts were duplicates). Step results use the `ansible.TaskStep` shape and publish to the same `ansible.progress` SSE topic, so the frontend op-log is identical for both write paths.

**Configuration writes → Ansible playbooks**
Idempotent mutations of config files, service state, and OS-managed resources (smb.conf, useradd, dataset property playbooks, ACLs) go through `ansible-playbook` with `ANSIBLE_STDOUT_CALLBACK=ndjson` (custom callback plugin at `playbooks/callback_plugins/ndjson.py`). The runner in `internal/ansible/runner.go` parses the ndjson output to extract task results.

**Data-plane writes → `internal/jobs` manager (direct exec)**
Long-running streaming operations like `zfs send | zfs recv` use `exec.Command` via `internal/jobs.Manager`, not Ansible. They can run for hours, far past any reasonable HTTP timeout, and need fire-and-forget dispatch with separate cancellation and durable status tracking. Ansible's request/response model and idempotency aren't a fit. The manager has two entry points: `Run(ctx, type, argv)` for single-process jobs, and `RunPipeline(ctx, type, left, right)` for two-process pipelines wired with an `os.Pipe` (no shell — keeps the runtime free of any `bash`/`dash`/`pipefail` portability concerns). Children are spawned in their own process group; cancel signals the whole group. The manager captures bounded stdout/stderr tails, persists job records under `internal/platform.StateDir(goos) + "/jobs"`, marks any job that was running at shutdown as `interrupted`, and publishes `jobs.update` SSE events on every state change.

Do not change this split without a good reason — the ops side keeps single-shot commands free of Python startup overhead and validator duplication; the Ansible side stays only where config-file rendering/service orchestration genuinely helps; the jobs side keeps long transfers from blocking HTTP. Ansible is a hard runtime dependency: `main.go` refuses to start without `ansible-playbook`, and the config/OS-resource features (SMB, users/groups, TLS, ACLs, dataset create/destroy/rename/property playbooks, scrub schedules, service control) use it.

### Adding a new operation

1. **Read**: add a function to `internal/zfs/zfs.go` and a typed handler `func (h *Handler) getX(ctx context.Context, p xParams) (X, error)` — return `errStatus(code, err)` for 4xx, any other error is a 500.
2. **Single-command write**: validate inputs in the handler, build the argv, run it via `h.runLocal(ctx, ops.Step{Name: ..., Argv: ...})`; on error return `localError(err, res)` (500 with the op-log steps).
3. **Configuration write** (owned config file / OS resource): add a playbook in `playbooks/`, wire it up in a handler using `h.runOp(ctx, ...)`; on error return `playbookError(err, out)`.
4. **Data-plane write (long-running)**: build the argv in the handler, dispatch via `h.jobs.Run(ctx, type, argv)`, return `202` with the job metadata. Status appears in the Jobs tab.
5. Register the route in `internal/api/routes.go:RegisterRoutes` with its OpenAPI metadata: `handle(reg, route("POST /api/v1/x", "group.method", "Summary", fails(http.StatusBadRequest, …), status(http.StatusCreated)), h.createX)`.
   - **Types:** every JSON handler is typed. Input is a struct with `path:`/`query:` fields (embed the shared param types) plus a `Body XRequest \`body:""\`` field. Output is a named struct. Never `map[string]any` or anonymous request structs; `TestNoUntypedRoutes` fails.
   - **Optional fields:** mark request fields a client may omit with `omitempty` so the spec's `required` list is right.
   - **`fails(...)`:** list every 4xx the handler or its helpers return. The integration suite fails on an undocumented one.
   - SDK names follow the #181 mapping.
   - Then run `make openapi` and commit `api/openapi.json` (CI fails when it is stale).
6. Add the UI in `static/app.js` (render function + fetch call) and `static/index.html` (button + dialog if needed).

---

## Conventions

### Playbooks

- All playbooks target `localhost` with `gather_facts: false`.
- Required extra vars must be documented in a header comment.
- Always include an `assert` task before any mutating command.
- Task names must be stable and readable — they are the op-log step names users see.
- The required extra vars in each playbook's header comment are the reference for that playbook; this file deliberately doesn't duplicate them.

### Frontend

- Vanilla JS, no framework, no build step.
- All data lives in the `state` object.
- Render functions are pure: they read from `state` and write innerHTML.
- **Event wiring uses delegation.** Render functions emit `data-action` plus payload `data-*` attributes; each stable container gets one `delegate(container, handlers, type?)` call (helper in `utils.js`), bound **once at module init** — never inside a render function. Handlers receive `(dataset, element, event)` and destructure the payload: `edit: ({ ds, type }) => …`. Do not re-bind listeners after `innerHTML` renders, do not use inline `onclick=`, do not export handlers via `window.*`. Singleton elements (dialog buttons, filters) keep plain `addEventListener`.
- Always escape user-controlled strings through `esc()` before inserting into HTML.
- The `api()` helper throws on non-2xx responses with the server's `error` field.
- **Build every API path with the `v1` tagged template** (`utils.js`): `api('GET', v1`/datasets/${name}/acl`)`. It prefixes `/api/v1` and percent-encodes every interpolated value, so names with `/` or `@` stay one path segment (#181). Pass raw names, never pre-encode, never interpolate a pre-built query string. Keep the method as a string literal right before the template: `TestFrontendPaths` checks every call against the server's routes.
- **Every write runs in an op-log session** (#184): `const op = startOp(title)` opens the op-log; send each write with `op.api(method, path, body)` (tags it with a fresh `X-Operation-ID` so only this operation's live steps are shown, and recovers the result if the connection drops); finish with `op.done(title, tasks, errorMsg, conflicts)` on success **and** failure. Never call plain `api()` for a write, and never use `toast()` alone for write actions.

#### UI consistency

- **Reuse existing button classes; don't invent new ones.** Row actions in tables share one visual language: subtle muted-grey buttons with a hover state. Look at what an adjacent row action uses (e.g. `.btn-rename`, `.btn-clone`, `.btn-acl`, `.btn-iscsi`) and either reuse that class or add the new selector to the existing comma-separated rule in `style.css`. Adding a `.btn-foo` class without a matching CSS rule means the button falls back to browser defaults — looks broken. This is a recurring miss; check before shipping.
- **Destructive actions are red** — use `.btn-danger` for full-size buttons and `.btn-cancel-job` / `.btn-del` for row actions. Don't introduce yet another red variant.
- **Primary dialog confirms are blue/accent** — use `.btn-primary`. Cancel/back is `.btn-secondary`.
- **Never set inline `style="background:..."` or `style="color:..."` on buttons.** If the design system doesn't have what you need, add a class to `style.css` (and reuse it).
- After adding any new button: open the relevant tab in a browser and visually verify it matches its siblings before declaring done.

### Logging

- Every request gets a `req_id` (from `X-Request-ID` or generated, echoed in the response) and every log line for that request carries it; audit lines (`auditLog`) record action, target and outcome for each write.

### Security

- Handlers validate every value with allow-list regexes and validators (`internal/api/handlers.go`) before it reaches an argv or a playbook.
- **Playbook values are data, never templates** (#214). The runner writes extra vars to a 0600 temp file whose root mapping is tagged `!unsafe` and passes `--extra-vars @file`, so Ansible never evaluates `{{ … }}` in a value and no value appears on the command line. Never add a code path that hands a playbook a template or puts values back on argv (`-e key=value`, inline JSON). Keep the tag on the root mapping: on ansible-core ≥ 2.19 a per-value `!unsafe "true"` becomes a boolean.
- The `static/` directory is served with `http.FileServer` — do not put secrets there.
- The service runs as root (required for ZFS). Do not expose it on a public interface without authentication in front of it.

---

## File Map

### Core

| File | Responsibility |
|------|----------------|
| `main.go` | Server setup, flag parsing, dependency check (`ansible-playbook` in PATH, `playbooks/` and `static/` dirs exist). Logging and request middleware live in `internal/logging/`. |
| `internal/zfs/zfs.go` | `ListPools`, `ListDatasets`, `ListSnapshots`, `IOStats` — all direct CLI calls |
| `internal/zfs/acl.go` | ACL reader for POSIX and NFSv4 — `GetDatasetACL` |
| `internal/ansible/runner.go` | `Runner.Run` / `RunStreaming` — executes a playbook with the ndjson callback and returns parsed `PlaybookOutput` |
| `internal/ansible/metrics.go` | OTEL counter/histogram for Ansible playbook runs (`ansible_runs_total`, `ansible_run_duration_seconds`) |
| `internal/operations/operations.go` | Write-operation registry (#184): `Begin`/`Step`/`Finish`/`Get`, per-op `seq`, tagged `ansible.progress` publish, bounded in-memory retention; `internal/api/operations.go` holds the `Operations` middleware (`X-Operation-ID`) and `GET /api/v1/operations/{id}` |
| `internal/ops/ops.go` | In-process executor for single-command writes — `Runner.Run(steps)`, argv exec, `ansible.TaskStep`-shaped results (#115) |
| `internal/apispec/` | Route registry + OpenAPI 3.1 generator (#181): `Registry.Handle` (route + metadata), `Spec()`, reflection schemas (`schema.go`) |
| `internal/api/routes.go` | `RegisterRoutes` — every API route with SDK group/method, summary and types; shared param types; `NewRegistry` (error type, tag descriptions, schema names) |
| `internal/api/route.go` | Typed handler adapter `handle`/`typed` (binds `path:`/`query:`/`body:` fields, writes the success status), `TaskResult`, error constructors `errStatus`/`opError`/`playbookError`/`localError` |
| `internal/api/spec_test.go` | Spec staleness (`make openapi`), `TestNoUntypedRoutes`, path/percent-encoding resolution, adapter tests |
| `api/openapi.json` | Generated OpenAPI spec — never edit by hand; `make openapi` |
| `internal/api/handlers.go` | Shared infra: validation helpers, `Handler` struct (with `authMu` RWMutex for config access), `runOp`, `writeJSON`/`writeError`/`writeRunOpError`, `getSysInfo`, `getVersion`, `getEvents`, `getSchema` |
| `internal/logging/handler.go` | `NewJournalHandler` — legacy slog handler with systemd journal priority prefixes (fallback path only) |
| `internal/logging/journal_exporter.go` | `JournalExporter` — OTEL log exporter reproducing the journald format (single producer for journald + OTLP logs, #49) |
| `internal/logging/apphandler.go` | `NewAppHandler` — process-wide slog handler: otelslog bridge + level gate + req_id injection |
| `internal/logging/middleware.go` | `RequestLogger` — HTTP middleware for per-request logging with req_id correlation; renames the otelhttp span to `METHOD /route` after mux dispatch |
| `internal/otel/otel.go` | `Init`/`Enabled`/`Tracer` — OTEL SDK setup: always-on log pipeline and meter provider (Prometheus reader → `Providers.MetricsHandler`, #168), env-gated trace provider and OTLP log/metric branches via autoexport (#49) |
| `internal/api/zfs_handlers.go` | Pool/dataset/snapshot reads, dataset create/props/rename/destroy, snapshots (create, clone, send, diff, delete), quotas, SMART, IOStat, chown, scrub, rewrite (background job) |
| `internal/api/pool_handlers.go` | Pool create/import/export, add data/cache/log/spare vdevs, device removal |
| `internal/api/refguard.go` | Reference guard (#183): refuses destroy/rename/mountpoint changes while shares, iSCSI targets or replication tasks still reference a dataset |
| `internal/api/autosnap_handlers.go` | Auto-snapshot status, takeover/release of the OS auto-snapshot daemon |
| `internal/api/replication_handlers.go`, `jobs_handlers.go` | Replication task CRUD/run/history; background job list/get/cancel/remove |
| `internal/api/auth_handlers.go`, `tls_handlers.go`, `service_handlers.go` | Password/username change; TLS status, self-signed, config, ACME; service status and control |
| `internal/api/device_handlers.go` | Block device list, `zpool replace`, device offline/online |
| `internal/blockdev/blockdev.go` | `List` — physical block devices (`/sys/block` on Linux, `geom disk list` on FreeBSD); `MarkInUse` — best-effort vdev→device matching |
| `internal/api/user_handlers.go` | User + group handlers, SSH key management |
| `internal/api/acl_handlers.go` | ACL handlers (POSIX + NFSv4) |
| `internal/api/smb_handlers.go` | SMB handlers: shares, users, homes, Time Machine, wsdd |
| `internal/api/iscsi_handlers.go` | iSCSI target handlers; FreeBSD: ctld init gate, read-modify-render of `ctl.conf` via `applyCtlConf` |
| `internal/iscsi/iscsi.go` | `Target` model, `Backend`, `ListTargets`, targetcli saveconfig reader |
| `internal/iscsi/ctld.go` | ctld (#186): `ParseCtld`/`LoadCtld`, `Import` (takes over an existing `ctl.conf`), `Render` (whole file from `ctl.conf.tmpl`), `CtldInitialized`, `Listeners` |
| `internal/api/httpmetrics.go` | `RecordHTTP` — OTEL request counter/latency histogram (called from `RequestLogger`), path normalisation |
| `internal/api/metrics.go` | Go runtime + build-info observable callback; `/metrics` serves the handler injected via `SetMetricsHandler` |
| `metrics_test.go` | Golden test pinning the `/metrics` exposition (names, types, labels, buckets, HELP) (#168) |
| `internal/system/system.go` | `ListUsers`, `ListGroups`, `UIDMin` — parses `/etc/passwd`, `/etc/group`, `/etc/login.defs` |
| `internal/auth/` | Config file, login page, sessions, argon2id passwords, rate limiting, trusted-proxy identity, auth middleware |
| `internal/jobs/manager.go` | Background job manager (`Run`/`RunPipeline`, process groups, persisted records, `jobs.update` SSE) |
| `internal/replication/` | Scheduled send/recv tasks: holds, incremental base selection, target pruning, task store |
| `internal/autosnap/`, `internal/scheduler/` | Native `com.sun:auto-snapshot*` execution and daemon takeover; 5-field cron parser and ticker |
| `internal/smb/` | Owned `smb.conf` and wsdd options: parse, render from templates (`smb.conf.tmpl`, `wsdd.default.tmpl`), usershare file reader |
| `internal/schema/`, `internal/platform/` | Dataset property metadata (writes `playbooks/vars/zfs_allowed_values.yml` at startup); OS-specific config/state paths |
| `internal/smart/smart.go` | `Collect` — calls `smartctl` for disk health data |
| `internal/broker/broker.go` | SSE broker — fan-out of events to connected clients |
| `internal/broker/poller.go` | Background poller that pushes pool/dataset/snapshot/iostat updates to the broker |

### Playbooks

`playbooks/*.yml` — one playbook per config/OS-resource write; each header comment lists its extra vars. Supporting files: `playbooks/callback_plugins/ndjson.py` (the ndjson stdout callback the runner parses), `playbooks/inventory/localhost` (`ansible_connection=local`, `ansible_python_interpreter=auto_silent`), `playbooks/vars/zfs_allowed_values.yml` (generated at startup by `internal/schema`).

### Frontend & install

| File | Responsibility |
|------|----------------|
| `static/index.html` | Page shell, all dialogs (dataset, snapshot, user, group, ACL, SMB, etc.), dataset drawer containers |
| `static/app.js` | Store subscriptions, tab switching, auth/config UI; entry point importing the per-tab modules under `static/js/` |
| `static/js/datasets.js` | Datasets table render (pool sections, usage bars, status chips, hover actions) + new-dataset dialog |
| `static/js/dataset-drawer.js` | Slide-in dataset detail drawer: summary, inline property editing, sharing/permissions/snapshots/danger sections (#63) |
| `static/js/dataset-dialogs.js` | Dataset-scoped dialogs: delete, rename, rewrite, per-user quotas, ACL, chown, NFS, SMB, iSCSI |
| `static/js/utils.js`, `store.js`, `loader.js` | `api`, `v1`, `esc`, `delegate`, `startOp`/op-log; the reactive `state` store; initial load, SSE subscription and polling fallback |
| `static/js/pools.js`, `snapshots.js`, `users.js`, `services.js`, `jobs.js`, `replication.js`, `autosnap.js` | Per-tab render functions and dialogs |
| `static/style.css` | Dark monospace theme, CSS variables in `:root` |
| `contrib/dumpstore.service` | systemd unit; binary at `/usr/local/lib/dumpstore/dumpstore` |
| `contrib/dumpstore.rc` | FreeBSD rc.d script |
| `install.sh` | Standalone install/uninstall script (wraps build + service setup) |
| `Makefile` | `build`, `install`, `uninstall`, `dev`, `openapi`, `release`, `test-integration`, `screenshots`, `vm-linux-*` / `vm-freebsd-*` |
