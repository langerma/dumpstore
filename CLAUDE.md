# dumpstore — Claude context

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

```bash
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
- No Go dependency for OpenAPI (#181): `internal/apispec` is stdlib reflection. The spec linter (`@redocly/cli`, pinned in `ci.yml`, config `redocly.yaml` + `.redocly.lint-ignore.yaml`) is CI-only Node tooling, never a runtime dependency.
- `go.opentelemetry.io/otel/exporters/prometheus` + `github.com/prometheus/client_golang` (`prometheus`, `promhttp`) + `github.com/prometheus/otlptranslator` — the meter's Prometheus reader that serves `/metrics` from a private registry (#168); CNCF projects. New metrics keep Prometheus-compatible names: counters without `_total` (the exporter adds it), durations as unit `s` (the exporter adds `_seconds`).

---

## Architecture

### Service ownership principle

**If dumpstore manages a service → full ownership, no exceptions.**
**If dumpstore does not manage a service → hands completely off.**

When dumpstore manages a service it:
- Owns the config file entirely — rendered from a template on every write, no block-patching
- Creates all referenced directories and prerequisite state as part of apply
- Restarts the service via the OS mechanism (systemd/rc.d) after every config change
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
Long-running streaming operations like `zfs send | zfs recv` use `exec.Command` via `internal/jobs.Manager`, not Ansible. They can run for hours, far past any reasonable HTTP timeout, and need fire-and-forget dispatch with separate cancellation and durable status tracking. Ansible's request/response model and idempotency aren't a fit. The manager has two entry points: `Run(type, argv)` for single-process jobs, and `RunPipeline(type, left, right)` for two-process pipelines wired with an `os.Pipe` (no shell — keeps the runtime free of any `bash`/`dash`/`pipefail` portability concerns). Children are spawned in their own process group; cancel signals the whole group. The manager captures bounded stdout/stderr tails, persists job records under `internal/platform.StateDir(goos) + "/jobs"`, marks any job that was running at shutdown as `interrupted`, and publishes `jobs.update` SSE events on every state change.

Do not change this split without a good reason — the ops side keeps single-shot commands free of Python startup overhead and validator duplication; the Ansible side stays only where config-file rendering/service orchestration genuinely helps; the jobs side keeps long transfers from blocking HTTP. Ansible remains a hard runtime dependency only for the config/OS-resource features (SMB, users/groups, TLS, ACLs, dataset property playbooks, scrub schedules, service control).

### Adding a new operation

1. **Read**: add a function to `internal/zfs/zfs.go` and a typed handler `func (h *Handler) getX(ctx context.Context, p xParams) (X, error)` — return `errStatus(code, err)` for 4xx, any other error is a 500.
2. **Single-command write**: validate inputs in the handler, build the argv, run it via `h.runLocal(ctx, ops.Step{Name: ..., Argv: ...})`; on error return `localError(err, res)` (500 with the op-log steps).
3. **Configuration write** (owned config file / OS resource): add a playbook in `playbooks/`, wire it up in a handler using `h.runOp(ctx, ...)`; on error return `playbookError(err, out)`.
4. **Data-plane write (long-running)**: build the argv in the handler, dispatch via `h.jobs.Run(type, argv)`, return `202` with the job metadata. Status appears in the Jobs tab.
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
- Task names must be stable — `RunAndGetStdout` looks them up by name.

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

### Security

- Input to Ansible extra-vars is checked for shell-special characters (``@;|&$\` ``) in handlers before the playbook call.
- The `static/` directory is served with `http.FileServer` — do not put secrets there.
- The service runs as root (required for ZFS). Do not expose it on a public interface without authentication in front of it.

---

## File Map

### Core

| File | Responsibility |
|------|----------------|
| `main.go` | Server setup, flag parsing, dependency check (`ansible-playbook` in PATH, `playbooks/` and `static/` dirs exist). Logging and request middleware live in `internal/logging/`. |
| `internal/zfs/zfs.go` | `ListPools`, `ListDatasets`, `ListSnapshots`, `IOStats` — all direct CLI calls |
| `internal/zfs/acl.go` | ACL helpers for POSIX and NFSv4 — `GetPosixACL`, `GetNFS4ACL` |
| `internal/ansible/runner.go` | `Runner.Run` — executes a playbook and returns parsed `PlaybookOutput`; `RunAndGetStdout` — convenience wrapper |
| `internal/ansible/metrics.go` | OTEL counter/histogram for Ansible playbook runs (`ansible_runs_total`, `ansible_run_duration_seconds`) |
| `internal/operations/operations.go` | Write-operation registry (#184): `Begin`/`Step`/`Finish`/`Get`, per-op `seq`, tagged `ansible.progress` publish, bounded in-memory retention; `api/operations.go` holds the `Operations` middleware (`X-Operation-ID`) and `GET /api/v1/operations/{id}` |
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
| `internal/api/zfs_handlers.go` | ZFS handlers: pools, datasets, snapshots, SMART, IOStat, chown, scrub, auto-snapshot, rewrite (background job) |
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
| `internal/smart/smart.go` | `ListDrives` — calls `smartctl` for disk health data |
| `internal/broker/broker.go` | SSE broker — fan-out of events to connected clients |
| `internal/broker/poller.go` | Background poller that pushes pool/dataset/snapshot/iostat updates to the broker |

### Playbooks

| File | Responsibility |
|------|----------------|
| `playbooks/zfs_dataset_create.yml` | Creates filesystem or volume; vars: `name`, `type`, `volsize`, `compression`, `quota`, `mountpoint` |
| `playbooks/zfs_dataset_destroy.yml` | Destroys dataset/volume; vars: `name`, optional `recursive` |
| `playbooks/zfs_dataset_set.yml` | Updates dataset properties; vars: `name`, optional `compression`, `quota`, `mountpoint`, `sharenfs`, `sharesmb` |
| `playbooks/acl_set_posix.yml` | Adds/updates a POSIX ACL entry; vars: `dataset`, `entry`, `recursive` |
| `playbooks/acl_remove_posix.yml` | Removes a POSIX ACL entry; vars: `mountpoint`, `entry`, `recursive` |
| `playbooks/acl_set_nfs4.yml` | Adds an NFSv4 ACL entry; vars: `dataset`, `entry`, `recursive` |
| `playbooks/acl_remove_nfs4.yml` | Removes an NFSv4 ACL entry; vars: `mountpoint`, `entry`, `recursive` |
| `playbooks/dataset_chown.yml` | Sets owner/group on a dataset mountpoint; vars: `mountpoint`, `owner`, `group` |
| `playbooks/user_create.yml` | Creates local Unix user; vars: `username`, `shell`, optional `uid`, `group`, `user_groups`, `password`, `create_group` |
| `playbooks/user_modify.yml` | Modifies local Unix user; vars: `username`, `uid`, optional `shell`, `groups`, `password` |
| `playbooks/user_delete.yml` | Deletes local Unix user; vars: `username`, `uid` |
| `playbooks/group_create.yml` | Creates local Unix group; vars: `groupname`, optional `gid` |
| `playbooks/group_modify.yml` | Modifies local Unix group; vars: `groupname`, `gid`, optional `new_groupname`, `new_gid` |
| `playbooks/group_delete.yml` | Deletes local Unix group; vars: `groupname`, `gid` |
| `playbooks/smb_usershare_set.yml` | Creates/updates a Samba usershare via `net usershare`; vars: `sharename`, `mountpoint` |
| `playbooks/smb_usershare_unset.yml` | Removes a Samba usershare; vars: `sharename` |
| `playbooks/smb_user_add.yml` | Registers user in Samba tdbsam; vars: `username`, `smb_password` |
| `playbooks/smb_user_remove.yml` | Removes user from Samba tdbsam; vars: `username` |
| `playbooks/iscsi_ctld_apply.yml` | FreeBSD: validates (`ctld -t`), deploys `/etc/ctl.conf` (0600), reloads/starts ctld, verifies ports + listeners, rolls back on failure; vars: `src`, `ctl_conf`, `expect_iqns`, `absent_iqns`, `expect_listeners`, optional `rollback_src`, `init` |
| `playbooks/inventory/localhost` | `ansible_connection=local`, `ansible_python_interpreter=auto_silent` |

### Frontend & install

| File | Responsibility |
|------|----------------|
| `static/index.html` | Page shell, all dialogs (dataset, snapshot, user, group, ACL, SMB, etc.), dataset drawer containers |
| `static/app.js` | Store subscriptions, tab switching, auth/config UI; entry point importing the per-tab modules under `static/js/` |
| `static/js/datasets.js` | Datasets table render (pool sections, usage bars, status chips, hover actions) + new-dataset dialog |
| `static/js/dataset-drawer.js` | Slide-in dataset detail drawer: summary, inline property editing, sharing/permissions/snapshots/danger sections (#63) |
| `static/js/dataset-dialogs.js` | Dataset-scoped dialogs: delete, rename, rewrite, per-user quotas, ACL, chown, NFS, SMB, iSCSI |
| `static/style.css` | Dark monospace theme, CSS variables in `:root` |
| `contrib/dumpstore.service` | systemd unit; binary at `/usr/local/lib/dumpstore/dumpstore` |
| `contrib/dumpstore.rc` | FreeBSD rc.d script |
| `install.sh` | Standalone install/uninstall script (wraps build + service setup) |
| `Makefile` | `build`, `install` (copies binary + playbooks + static, enables service), `uninstall`, `clean` |
