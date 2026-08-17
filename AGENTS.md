# dumpstore — OpenCode instructions

## Quick start

```bash
go build ./...
go vet ./...
go test ./...              # unit tests (no external deps)
go test -tags integration ./tests/integration/...  # requires live Lima VM
```

Before committing: **build**, **vet**, **test** must pass.  
Run a single test: `go test -run TestFoo ./internal/api/...`

---

## Core principles

- **Simplicity first**: minimal code impact, no temporary fixes.
- **Full ownership or hands off**: if dumpstore manages a service → own the config completely (template-rendered), otherwise read-only/integrate.
- **No half-measures**: block-patching is forbidden. Either render the full config file or don't touch it.

## Service ownership model

| Service | Managed? | Config file | Restart mechanism |
|---------|----------|-------------|-------------------|
| Samba   | ✅ full  | `/etc/samba/smb.conf` / `/usr/local/etc/smb4.conf` | `systemctl restart smbd` / `service samba_server restart` |
| NFS     | ✅ via ZFS | ZFS `sharenfs` property (ZFS manages `/etc/exports`) | automatic on `zfs set` |
| iSCSI   | ✅ via CLI | `targetcli saveconfig` / `/etc/ctl.conf` | `targetcli` / `service ctld restart` |
| TLS     | ✅ full  | `dumpstore.conf` cert fields | dumpstore reload |
| Users/Groups | —   | OS source of truth (`useradd`/`groupadd`) | n/a |

Sub-features for managed services (shares, home dirs, Time Machine targets) are gated behind an **init gate** — disabled until the service has been bootstrapped.

## The four execution lanes

1. **Reads** (`internal/zfs`, `internal/system`, `internal/smart`): direct CLI via `exec.Command(zpool/zfs/smartctl)` — fast, no Python.
2. **Single-command writes** (`internal/ops`): in-process argv exec, no shell — validated in Go handlers, ~ms latency.
3. **Config-file / OS-resource writes**: `ansible-playbook` with ndjson callback (config-rendering services: SMB users, TLS certs, ACLs, dataset property playbooks). Python dependency required only for this lane.
4. **Long-running data-plane** (`internal/jobs`): background process groups via `exec.Command` — `zfs send \| recv`, `zfs rewrite` (hours-long, 202 + polling).

Both write lanes report the same `tasks: [...]` step shape and stream live progress over the `ansible.progress` SSE topic.

## Build & install

```bash
go build -buildvcs=false -ldflags="-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o dumpstore .

sudo make install         # detects OS, registers service
sudo make uninstall       # removes service and files
make dev                  # stub mode on macOS (no ZFS/Ansible)
make release VERSION=v1.2.3
```

## Local development

- **Stub mode** (`make dev`): fake CLI stubs in `dev/bin/` intercept zfs, zpool, ansible — full UI renders on macOS without ZFS.
- **VM mode**: Lima VMs with ZFS+Ansible pre-provisioned.

```bash
make vm-linux-start      # creates Ubuntu 24.04 VM (port 8080)
make vm-linux-deploy     # pack, copy, `make install` in VM
make vm-linux-ssh        # open shell in VM
make test-integration    # drive deployed API over HTTP

# FreeBSD VM (port 8081)
make vm-freebsd-start
make vm-freebsd-deploy
```

## Testing

- **Unit tests**: `go test ./...` — no external dependencies.
- **Integration tests**: `go test -tags integration -count=1 -timeout 30m -v ./tests/integration/...` — requires deployed VM, tests auth, dataset/snapshot lifecycle, scrub, user quotas, send/recv jobs, pool lifecycle.

## Architecture notes

### Request flow for writes

```
Browser (POST /api/datasets) → zfs_handlers.go: createDataset()
  └─ validate inputs in Go → build argv
     └─ internal/ops.Run(steps) [in-process] OR ansible-playbook (config-file writes)
        └─ return 201 + tasks → browser shows op-log dialog
```

### Read-only integration pattern

- dumpstore **reads**, displays, links out — never embeds dashboards/log viewers/alerting pipelines for integrate domains.
- Feature test: "Does this make dumpstore better at running ZFS+shares, or does it belong to another tool?"

### Frontend conventions

- Vanilla JS, no build step. All data lives in `state` object.
- Event delegation: render emits `data-action` + payload `data-*`; bound once via `delegate(container, handlers)` in module init — never re-bind after `innerHTML`.
- Escape user input with `esc()` before innerHTML.
- Always show `showOpLog()` dialog after Ansible-backed write operations (success or failure).

### UI button classes

Reuse existing styles; don't invent new ones:
- Destructive row actions: `.btn-cancel-job` / `.btn-del` (red)
- Full-size destructive: `.btn-danger` (red)
- Primary confirm: `.btn-primary` (blue/accent)
- Cancel/back: `.btn-secondary`

Check `style.css` before adding a new button class.

### Logging & observability

Two complementary exports:
- **Prometheus** (`GET /metrics`): Go runtime, HTTP req counters/latency histograms, Ansible run metrics — always on.
- **OpenTelemetry**: set `OTEL_EXPORTER_OTLP_ENDPOINT` (and related env vars) → push traces, logs (journald stream with trace_id correlation), metrics to collector. No-op without env vars.

Request ID correlation: every request gets `req_id` (from `X-Request-ID` header or generated); logged on all lines for that request.

## Playbook conventions

- Target `localhost` with `gather_facts: false`.
- Always include an `assert` task before any mutating command.
- Task names must be stable — `RunAndGetStdout` looks them up by name.
- Document required extra vars in a header comment.

## Security reminders

- dumpstore runs as root (required for ZFS).
- `static/` is served with `http.FileServer` — never put secrets there.
- If no password configured, service binds to `127.0.0.1` only.
- Input to Ansible extra-vars is checked for shell-special characters (`@;|&$\`) in handlers.

## File map (core)

| File | Responsibility |
|------|----------------|
| `main.go` | Server setup, flags, startup dependency checks |
| `internal/zfs/zfs.go` | ListPools, ListDatasets, ListSnapshots, IOStats, PoolStatuses |
| `internal/ansible/runner.go` | Run(playbook, extraVars) → PlaybookOutput; ndjson parsing |
| `internal/api/handlers.go` | Handler struct, RegisterRoutes, validation helpers |
| `internal/ops/ops.go` | In-process argv exec for single-command writes |
| `internal/jobs/manager.go` | Background job manager (fire-and-forget data-plane ops) |
| `playbooks/*.yml` | Ansible playbooks for config-file writes |
| `static/index.html`, `static/app.js`, `static/js/*` | Vanilla JS SPA, store-based state management |

## Common pitfalls

- **Never block-patch configs**: smb.conf, ACLs, dataset properties → full template render via playbook.
- **Don't use shell pipes in Ansible**: keep the runtime free of bash/dash/pipefail portability concerns; use `os.Pipe()` directly for pipelines.
- **Long-running jobs are not Ansible**: `zfs send \| recv` uses `internal/jobs`, not playbooks (hours-long, 202 response).
- **Plan before acting**: enter plan mode for any task with 3+ steps or architectural decisions. Use subagents for research/parallel analysis.

## Documentation

- Keep README.md, docs/index.html, wiki/ up to date when routes/architecture/features change.
- Document new features in logseq via mcp-logseq.
