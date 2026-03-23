# dumpstore — Code Review TODO

## Critical

- [x] **`runner.go`: Unbounded scanner buffer** — Added `scanner.Buffer(buf, 4 MB)` cap on NDJSON line reads.
- [x] **Passwords in plaintext without TLS enforcement** — Documented in `SECURITY.md` with recommended mitigations (reverse proxy, SSH tunnel, VPN).
- [x] **No rate limiting on password endpoints** — Documented in `SECURITY.md`; mitigation is proxy-layer rate limiting.

## High

- [x] **`handlers.go`: Dataset existence not checked before playbook** — `datasetExists` helper added; `deleteDataset`, `setDatasetProps`, `setDatasetOwnership`, `setACLEntry`, `removeACLEntry` all return 404 before running the playbook.
- [x] **`handlers.go`: ACL principal regex too loose** — `aclSafeRe` now enforces per-field `@` rules: at most one, not leading, trailing only for all-uppercase NFSv4 well-known principals (OWNER@, GROUP@, EVERYONE@).
- [x] **`acl.go`: `DatasetHasACL` silently returns false when tools missing** — Signature changed to `(bool, error)`; both tool errors are now propagated. Call site in `getACLStatus` logs `slog.Warn` and falls back to `acltype` property.
- [ ] **`playbooks/user_create.yml`: No rollback on partial failure** — skipped; Ansible lacks native rollback and the orphaned-group risk is low enough to defer.
- [x] **`handlers.go`: Group members not verified to exist** — `modifyGroup` now calls `system.ListUsers()` and rejects unknown member names with a 400 listing the offenders.

## Medium

- [x] **`handlers.go`: JSON decoder accepts trailing garbage** — `decodeJSON` helper added; does a second `Decode` and rejects unless it returns `io.EOF`. All 14 call sites updated.
- [x] **`handlers.go`: `reZFSName` allows numeric-only path components** — Regex first-char class tightened from `[a-zA-Z0-9]` to `[a-zA-Z]` for each path component.
- [x] **`zfs.go`: `GetMountpointOwnership` symlink behavior undocumented** — Added `-L` flag explicitly and documented the follow-symlink choice in the function comment.
- [x] **`handlers.go`: Insufficient error context on playbook failure** — `lastTaskName` tracked during NDJSON scan; included in the fallback non-zero-exit error message.

## Features

- [x] **`app.js`: Reactive store micro-layer** — Replace the single `state` object + manual `renderX()` calls with a ~50-line `createStore` / `subscribe` pattern. `store.set('datasets', data)` automatically calls all subscribers for that key; eliminates the full-tab re-render on every SSE tick, fixes the innerHTML clobber problem (open dialogs, focused inputs, scroll position get destroyed on each poll), and makes future feature tabs self-contained. Do before adding more tabs — migration cost grows linearly with tab count.

## High (round 2)

- [x] **No test coverage** — Zero `*_test.go` files exist. Add unit tests for regex validators (`reZFSName`, `reUnixName`, `reSnapLabel`), NDJSON parser in `runner.go`, and ZFS CLI output parsing in `zfs.go`. Then add integration tests for at least one create/delete cycle (dataset or user). This is the single biggest quality gap.

- [x] **No CI build/lint pipeline** — Only `check-docs.yml` runs in CI. Add a workflow that runs `go build ./...`, `go vet ./...`, and optionally `golangci-lint`. Prevents broken merges and catches issues early.

- [ ] **`handlers.go`: Password fields bypass `safePropertyValue`** — `createUser`, `modifyUser`, and `setSMBPassword` pass the `password` field directly to Ansible extra-vars without calling `safePropertyValue`. A password containing newlines corrupts the `smbpasswd` stdin input (`playbooks/user_create.yml:79`) because the `stdin:` field splits on newlines. Validate password fields reject `\n` / `\r` before use.

## Medium (round 2)

- [ ] **`handlers.go`: Split into domain-specific files** — At 2,150 LOC with 54 handlers, `handlers.go` is a monolith. Split into `zfs_handlers.go`, `user_handlers.go`, `acl_handlers.go`, `smb_handlers.go`, `iscsi_handlers.go` etc. No logic changes — just file boundaries for navigability.

- [ ] **`app.js`: Split into per-tab modules** — At 2,460 LOC with 77 functions, `app.js` is hard to navigate. Group render functions and event handlers by tab/feature into separate files or ES modules.

- [ ] **Request ID correlation in logs** — HTTP middleware generates a UUID per request and stores it in context; all `slog` calls inside handlers use `slog.InfoContext` so every log line carries `req_id`. Lets you reconstruct a full request lifecycle from logs when concurrent requests overlap. No new dependencies — stdlib `context` + `log/slog` only.

- [ ] **`broker.go`: SSE subscriber channel is only 8 deep; slow clients silently drop messages** — `internal/broker/broker.go` allocates a `chan []byte` of capacity 8 per subscriber. When a client is slow the channel fills and new events are dropped with a warn log only. The frontend never knows it missed an update and shows stale state. Either increase the buffer, close lagging subscribers, or add a sequence number so the client can detect a gap and force a full refresh.
- [ ] **`handlers.go`: `createISCSITarget` allows CHAP password through `safePropertyValue` but user/SMB passwords don't** — Inconsistency: iSCSI CHAP password is validated with `safePropertyValue` (`handlers.go:2061`) but Unix and SMB passwords are not. Unify by running all password fields through the same validator.

## Low (round 2)

- [ ] **OpenTelemetry tracing** — Instrument the HTTP handler, Ansible runner, and ZFS read calls with OTEL spans. Deferred until a trace collector (Jaeger, Grafana Tempo) is available alongside the service — adds a dependency and dead code otherwise. When implemented: one span per HTTP request, child spans for `runner.Run` and each `zfs`/`zpool` exec call, span attributes for playbook name, dataset name, exit code.

- [ ] **No audit logging** — User/group/dataset mutations aren't logged with any operator identity. Compliance gap.
- [ ] **ACL remove doesn't verify entry exists first** — Calling `setfacl -x` on a nonexistent entry fails silently. Pre-check with `GetDatasetACL()`.
- [ ] **`handlers.go`: Confusing error messages for missing datasets** — Many handlers let the playbook surface the error; a pre-existence check would give cleaner UX.
- [ ] **`handlers.go`: No upper-bound validation on numeric ZFS properties** — `quota`, `recordsize`, etc. accept arbitrary strings like `"99999999999T"` that are syntactically valid but fail at the ZFS layer with a cryptic error. Parse and range-check numeric property values before sending to the playbook.
- [ ] **`app.js`: No client-side name validation in create dialogs** — Dataset and snapshot create dialogs send the name to the backend without checking it against the allowed regex first. Results in a round-trip error instead of immediate inline feedback. Mirror `reZFSName` / `reSnapLabel` in the JS dialog submit handler.
- [ ] **`app.js`: No visual indicator when SSE degrades to polling** — When `EventSource` fails and the client falls back to 30 s REST polling (`startSSE` fallback path), there is no UI indicator. Users see stale data with no explanation. Add a subtle status badge (e.g. "live" vs "polling") in the header.
- [ ] **Consistent dataset pre-checks across handlers** — Some handlers call `datasetExists()` before the playbook, others let Ansible surface the error. Standardize so all mutating handlers pre-check and return a clear 404.
