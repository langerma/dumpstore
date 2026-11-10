# Observability

dumpstore **exports** telemetry to the monitoring stack you already run — it never embeds dashboards, log viewers, or alerting (see the scope boundary in [[Architecture]]). There are three outputs:

| Output | Transport | On by default | Configure with |
|---|---|---|---|
| Prometheus metrics | pull, `GET /metrics` | yes | nothing; scrape it |
| OpenTelemetry traces, logs, metrics | push, OTLP (HTTP or gRPC) | no — nothing is exported until an endpoint is configured | standard `OTEL_*` environment variables |
| Local logs | journald (Linux) / `/var/log/dumpstore.log` (FreeBSD) | yes | `-debug`, `-log-stdout` |

The **System** tab's info card shows whether OTEL export is active, with the endpoint, protocol, and service name (header env vars, which may carry credentials, are never shown).

---

## Prometheus

`GET /metrics` serves the Prometheus text format. It bypasses login by default because `/metrics` is in `unprotected_paths` in `dumpstore.conf`; remove it there to require a session (or a trusted proxy's `X-Remote-User`).

```yaml
# prometheus.yml
scrape_configs:
  - job_name: dumpstore
    static_configs:
      - targets: ["nas.example:8080"]
```

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `dumpstore_build_info` | gauge (always 1) | `version`, `goversion` | Build identification |
| `http_requests_total` | counter | `method`, `path`, `status` | API requests (`/api/…` and `/metrics` only; static files are not counted) |
| `http_request_duration_seconds` | histogram | `method`, `path` | API request latency |
| `ansible_runs_total` | counter | `playbook`, `status` | Configuration-write playbook runs (`ok` / `failed`) |
| `ansible_run_duration_seconds` | histogram | `playbook` | Playbook run duration |
| `dumpstore_op_runs_total` | counter | `op`, `status` | In-process single-command writes (e.g. `op="zpool scrub"`) |
| `dumpstore_op_duration_seconds` | histogram | `op` | Duration of those commands |
| `go_goroutines`, `go_memstats_*`, `go_gc_cycles_total`, `go_gc_pause_ns_total` | gauge / counter | — | Go runtime |
| `process_start_time_seconds` | gauge | — | Process start (Unix time) |

The `path` label is normalised to keep cardinality bounded: `/api/<resource>` or `/api/<resource>/{name}` — e.g. `DELETE /api/datasets/tank/vm1` is counted as `/api/datasets/{name}`. `op` is the command plus subcommand (`zfs destroy`), never its arguments.

dumpstore does **not** export ZFS pool, dataset, or disk metrics; use [`node_exporter`](https://github.com/prometheus/node_exporter)'s ZFS collector or [`zfs_exporter`](https://github.com/pdf/zfs_exporter) for those.

---

## OpenTelemetry

Set the standard OTLP environment variables on the service and restart it. Export is enabled when `OTEL_EXPORTER_OTLP_ENDPOINT` or one of the per-signal endpoints is set (and `OTEL_SDK_DISABLED` is not `true`); otherwise tracing is a no-op and metrics are only served on `/metrics`. The OTEL log SDK always runs — it is what writes the local logs — but without an endpoint it has no OTLP branch.

```bash
# Linux (systemd) — the shipped unit has these lines commented out
sudo systemctl edit dumpstore
# [Service]
# Environment=OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.example:4318
# Environment=OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
# Environment=OTEL_SERVICE_NAME=dumpstore
sudo systemctl restart dumpstore

# FreeBSD (rc.d) — rc.subr passes ${name}_env to the daemon
sysrc dumpstore_env="OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.example:4318"
service dumpstore restart
```

| Variable | Default | Notes |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Enables export of all three signals |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` | `grpc` also supported (port 4317 by convention) |
| `OTEL_SERVICE_NAME` | `dumpstore` | `service.name` resource attribute |
| `OTEL_EXPORTER_OTLP_{TRACES,LOGS,METRICS}_ENDPOINT` | — | Per-signal endpoints, e.g. to send only traces |
| `OTEL_EXPORTER_OTLP_HEADERS` | — | e.g. `Authorization=Bearer …` for hosted collectors |
| `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` | parent-based always-on | Standard sampler config |
| `OTEL_SDK_DISABLED` | `false` | `true` turns everything off again |

All other standard OTEL SDK variables are honoured. Works with any OTLP receiver: Grafana Alloy, the OpenTelemetry Collector, Tempo/Loki/Mimir, Jaeger, Honeycomb, …

If the collector is unreachable, OTLP batches are retried and then dropped in the background; requests are never slowed down and local logs are never affected.

### Traces

| Span | Kind | Attributes |
|---|---|---|
| `METHOD /route/pattern` (e.g. `DELETE /api/datasets/{name...}`) | server, one per request | standard `http.*`, `req_id`. Requests that never reach a route are named `METHOD (unauthenticated)` or `METHOD (unmatched)` with `dumpstore.short_circuit`, never the raw path |
| `ansible.playbook` | child of the request | `ansible.playbook`, `ansible.extra_var_keys` (names only — values such as passwords are never recorded) |
| `ops.<command> <subcommand>` (e.g. `ops.zfs destroy`) | child of the request | `ops.step`, `ops.argv` |
| `job.<type>`: `job.snapshot.send`, `job.dataset.rewrite`, `job.replication.run` | root span, **linked** to the request that started it (jobs outlive the request) | `job.type`, `job.id`, `job.argv`, `job.status`, `job.exit_code` |
| `replication.run` | root span per scheduled run (its transfer is a `job.replication.run` span) | `replication.task_id`, `replication.source`, `replication.target` |
| `autosnap.run` | root span per scheduler fire | `autosnap.bucket` |

ZFS read commands (the `zfs`/`zpool` calls behind `GET` endpoints and the poller) are not traced (planned in [#167](https://github.com/langerma/dumpstore/issues/167)).

### Logs

With OTLP enabled, every log line — the same records written locally — is also exported, with `trace_id`/`span_id` set when the line belongs to a traced request, so you can jump from a trace to its logs and back. For OTLP-only setups, start dumpstore with `-log-stdout=false` to stop local logging; with no OTLP endpoint configured that would discard all logs, so dumpstore warns at startup.

### Metrics

Prometheus and OTLP share one meter provider, so OTLP metrics carry everything in the Prometheus table above, under the OTEL instrument names: `http_requests`, `http_request_duration` (unit `s`), `ansible_runs`, `ansible_run_duration`, `dumpstore_op_runs`, `dumpstore_op_duration`, and the `go_*` / `process_start_time_seconds` / `dumpstore_build_info` series as-is. The Prometheus exporter adds the `_total` and `_seconds` suffixes. On top of that, OTLP carries the OTEL Go runtime instrumentation (`go.memory.*`, `go.goroutine.count`, …) and otelhttp's `http.server.*` request metrics. With OTLP enabled, those also appear on `/metrics` (e.g. `go_memory_used_bytes`, `http_server_request_duration_seconds`).

---

## Local logs

- **Linux** — stdout to the systemd journal, with syslog priority prefixes (`<3>` error … `<7>` debug) so `journalctl -p warning -u dumpstore` works.
- **FreeBSD** — `daemon(8)` writes stdout to `/var/log/dumpstore.log` (`dumpstore_logfile` in `rc.conf`).
- `-debug` lowers the level to debug.

Lines are `key=value` (slog text format). Every line belonging to a request carries its `req_id`, taken from an upstream `X-Request-ID` header if present (nginx, Traefik) and echoed back on the response; otherwise generated. When the request is traced, `trace_id` and `span_id` are appended.

```text
time=2026-11-09T22:37:30.216+01:00 level=INFO msg=request req_id=53a61f4298899664 method=POST path=/api/iscsi/init status=200 duration_ms=11024 remote=127.0.0.1:27429
```

### Audit lines

Every mutating operation writes one `msg=audit` line:

| Field | Example |
|---|---|
| `action` | `dataset.create`, `smb.init`, `iscsi.create`, `user.delete`, … |
| `target` | the resource, e.g. `tank/vm1` |
| `outcome` | `ok` or `err` (then also `error=…`) |
| `remote_ip` | client address |
| `req_id` | links the audit line to the request's other log lines (and, via `trace_id`, to its trace) |

```text
time=2026-11-09T22:37:30.216+01:00 level=INFO msg=audit remote_ip=127.0.0.1:27429 action=iscsi.init target="" outcome=ok req_id=53a61f4298899664
```

To keep an audit trail, ship these lines to your log store (journald → Loki/Elasticsearch, or OTLP logs) and filter on `msg=audit`.

---

See [[Architecture]] → Observability for how the pipeline is built (single-producer log pipeline, span naming, context handling).
