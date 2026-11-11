# Contributing to dumpstore

Thanks for your interest in contributing. dumpstore is a focused project with a clear philosophy — please read this before opening a PR.

## Philosophy

- **Minimal, justified Go dependencies.** Stdlib first — a dependency needs stdlib-grade governance and a reason the stdlib can't cover. Current exceptions: `golang.org/x` (argon2id hashing, terminal prompting), the CNCF-governed OpenTelemetry SDK, and the Prometheus client/exporter modules that serve `/metrics` (see the dependency list in [AGENTS.md](AGENTS.md)). If you think you need anything else, find a way without it.
- **No frameworks on the frontend.** Vanilla JS, no build step, no bundler.
- **Minimal footprint on the host.** Reads are direct CLI calls. Writes take one of three paths: single commands in-process (`internal/ops`), config files and OS resources via Ansible playbooks, long-running transfers via the jobs manager. Don't blur those lines (see *Read / write split* in [AGENTS.md](AGENTS.md)).

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

## Conventions

[AGENTS.md](AGENTS.md) is the single reference for how the code is organised and how changes are made — for human contributors as much as for coding agents. Read at least these sections before your first PR:

- **Adding a new operation**: typed handlers, route registration with OpenAPI metadata, `make openapi`
- **Read / write split** and **Service ownership principle**
- **Conventions → Playbooks / Frontend / UI consistency / Security**: including `startOp` for every write and the `v1` path template

## Code review bar

PRs are merged when they are correct, simple, and consistent with the existing style. "Works on my machine" is not enough — explain how you tested it.

## Reporting issues

Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md) for bugs and the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md) for ideas.
