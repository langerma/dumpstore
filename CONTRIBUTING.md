# Contributing to dumpstore

Thanks for your interest in contributing. dumpstore is a focused project with a clear philosophy — please read this before opening a PR.

## Philosophy

- **Minimal, justified Go dependencies.** Stdlib first — a dependency needs stdlib-grade governance and a reason the stdlib can't cover. Current exceptions: `golang.org/x` (argon2id hashing, terminal prompting) and the CNCF-governed OpenTelemetry SDK (env-gated, no-op by default). If you think you need anything else, find a way without it.
- **No frameworks on the frontend.** Vanilla JS, no build step, no bundler.
- **Minimal footprint on the host.** Reads go through direct CLI calls; writes go through Ansible playbooks. Don't blur that line.

## Getting started

```bash
git clone https://github.com/langerma/dumpstore
cd dumpstore
go build ./...   # must pass
go vet ./...     # must pass
```

You need `ansible-playbook` in your PATH and a machine with ZFS to test write operations end-to-end. Read-only development can be done on any Linux box.

## Workflow

1. **Open an issue first** for anything non-trivial. Alignment before code saves everyone time.
2. **Create a feature branch**: `git checkout -b feat/<name>` or `fix/<name>`.
3. Make your changes. Keep them focused — one concern per PR.
4. Update docs if your change affects routes, features, or architecture:
   - `README.md`
   - `docs/index.html`
   - relevant page in `wiki/`
5. Open a PR against `main` using the pull request template.

## Adding a new operation

| Type | What to do |
|------|-----------|
| Read | Add a function to `internal/zfs/zfs.go` + handler in `internal/api/handlers.go` |
| Write | Add a playbook in `playbooks/` + handler wired via `h.runner.Run(...)` |
| Both | Register the route in `handlers.go:RegisterRoutes` |
| UI | Render function in `static/app.js`, markup in `static/index.html` |

See [CLAUDE.md](CLAUDE.md) for detailed conventions on playbooks and the frontend.

## Playbook conventions

- Target `localhost`, `gather_facts: false`.
- Always include an `assert` task before any mutating command.
- Task names must be stable — the Go runner looks them up by name.
- Required extra vars must be documented in a header comment.

## Frontend conventions

- All data lives in the `state` object.
- Render functions are pure: read `state`, write `innerHTML`.
- Always escape user-controlled strings via `esc()` before inserting into HTML.
- Always show the Ansible op-log dialog (`showOpLog`) after every write operation — never `toast()` alone.

## Code review bar

PRs are merged when they are correct, simple, and consistent with the existing style. "Works on my machine" is not enough — explain how you tested it.

## Reporting issues

Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md) for bugs and the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md) for ideas.
