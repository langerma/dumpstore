# dumpstore — Claude context

## Build & check

```bash
go build ./...   # must always pass before committing
go vet ./...     # must always pass
```

No external Go dependencies. Standard library only.

## Architecture rules

**Read ops → `internal/zfs` package (direct exec)**
`zpool`/`zfs` CLI commands are called directly via `exec.Command` for low latency. No Ansible for reads.

**Write ops → Ansible playbooks**
Create/destroy operations go through `ansible-playbook` with `ANSIBLE_STDOUT_CALLBACK=json`. The runner in `internal/ansible/runner.go` parses the JSON callback output to extract task results.

Do not change this split without a good reason — it exists to avoid Ansible's Python startup overhead on every API call.

## Adding a new operation

1. If it's a **read**: add a function to `internal/zfs/zfs.go` and a handler in `internal/api/handlers.go`.
2. If it's a **write**: add a playbook in `playbooks/`, wire it up in `handlers.go` using `h.runner.Run(...)`.
3. Register the route in `handlers.go:RegisterRoutes`.
4. Add the UI in `static/app.js` (render function + fetch call) and `static/index.html` (button + dialog if needed).

## Playbook conventions

- All playbooks target `localhost` with `gather_facts: false`.
- Required extra vars must be documented in a header comment.
- Always include an `assert` task before any mutating command.
- Task names must be stable — `RunAndGetStdout` looks them up by name.

## Frontend

- Vanilla JS, no framework, no build step.
- All data lives in the `state` object.
- Render functions are pure: they read from `state` and write innerHTML.
- Always escape user-controlled strings through `esc()` before inserting into HTML.
- The `api()` helper throws on non-2xx responses with the server's `error` field.

## Security

- Input to Ansible extra-vars is checked for shell-special characters (`@;|&$\``) in handlers before the playbook call.
- The `static/` directory is served with `http.FileServer` — do not put secrets there.
- The service runs as root (required for ZFS). Do not expose it on a public interface without authentication in front of it.

## File map

| File | Responsibility |
|------|---------------|
| `main.go` | Server setup, flag parsing, dependency check (`ansible-playbook` in PATH, `playbooks/` and `static/` dirs exist) |
| `internal/zfs/zfs.go` | `ListPools`, `ListDatasets`, `ListSnapshots`, `IOStats` — all direct CLI calls |
| `internal/ansible/runner.go` | `Runner.Run` — executes a playbook and returns parsed `PlaybookOutput`; `RunAndGetStdout` — convenience wrapper |
| `internal/api/handlers.go` | All HTTP handlers + input validation + `writeJSON` / `writeError` helpers |
| `playbooks/zfs_dataset_create.yml` | Creates filesystem or volume; vars: `name`, `type`, `volsize`, `compression`, `quota`, `mountpoint` |
| `playbooks/zfs_snapshot_create.yml` | Creates snapshot; vars: `dataset`, `snapname`, `recursive` |
| `playbooks/zfs_snapshot_destroy.yml` | Destroys snapshot; vars: `snapshot`, `recursive` |
| `playbooks/inventory/localhost` | `ansible_connection=local`, `ansible_python_interpreter=auto_silent` |
| `static/index.html` | Page shell, dialogs (new dataset, new snapshot) |
| `static/app.js` | State, fetch, render functions for pools/datasets/snapshots/iostat, dialog wiring |
| `static/style.css` | Dark monospace theme, CSS variables in `:root` |
| `dumpstore.service` | systemd unit; binary at `/usr/local/lib/dumpstore/dumpstore` |
| `Makefile` | `build`, `install` (copies binary + playbooks + static, enables service), `uninstall`, `clean` |
