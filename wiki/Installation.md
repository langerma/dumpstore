# Installation

## Requirements

|                        | Linux                                                     | FreeBSD                                      |
|------------------------|-----------------------------------------------------------|----------------------------------------------|
| ZFS                    | `zfsutils-linux` or equivalent                            | built-in                                     |
| Ansible                | `ansible` package (Python 3)                              | `py311-ansible` or equivalent                |
| Service manager        | systemd                                                   | rc.d (via `daemon(8)`)                       |
| S.M.A.R.T. (optional)  | `smartmontools`                                           | `smartmontools` pkg                          |
| POSIX ACLs (optional)  | `acl` (`getfacl`/`setfacl`)                               | n/a — ZFS on FreeBSD uses NFSv4 ACLs         |
| NFSv4 ACLs (optional)  | `nfs4-acl-tools` (`nfs4_getfacl`/`nfs4_setfacl`)          | base system (`getfacl`/`setfacl`)            |
| NFS sharing (optional) | `nfs-kernel-server` (Debian) or `nfs-utils` (RHEL/Fedora) | base system                                  |
| SMB sharing (optional) | `samba` (`smbd`, `net`, `pdbedit`); Time Machine shares need the `fruit`/`catia`/`streams_xattr` VFS modules — `samba-vfs-modules` on Debian/Ubuntu (installed by default as a recommendation of `samba`, missing with `--no-install-recommends`), part of `samba` on RHEL/Fedora | `samba` pkg (the port builds its VFS modules in; no separate package) |
| Windows discovery (optional) | `wsdd-server` (Debian/Ubuntu — the `wsdd` package alone has no service), `wsdd` elsewhere | `py3*-wsdd` pkg |
| iSCSI (optional)       | `targetcli-fb` (`targetcli`)                              | built-in `ctld`                              |
| TLS / ACME (optional)  | `openssl` (usually pre-installed); `lego` for ACME        | same                                         |
| Build                  | Go 1.26+                                                  | Go 1.26+                                     |

ZFS and Ansible are the hard runtime requirements — the server checks for `zfs`, `zpool`, and `ansible-playbook` in PATH at startup and refuses to start with a clear error if any is missing. Go is only needed to build (`make install` installs it if absent). On FreeBSD, the System tab warns when the ZFS kernel module is not loaded even though the userland tools are installed. The Installed Software card on the System tab shows which optional tools are present and which feature each one unlocks; dialogs for a feature whose tool is missing show an error. To export telemetry to an OTLP collector, see [Observability](#observability).

### Optional packages

Install only what you need:

```bash
# Debian/Ubuntu — POSIX ACLs
apt install acl

# Debian/Ubuntu — NFS sharing
apt install nfs-kernel-server
systemctl enable --now nfs-server

# Debian/Ubuntu — NFSv4 ACLs
apt install nfs4-acl-tools

# Debian/Ubuntu — SMB sharing (+ Windows network discovery)
apt install samba wsdd-server

# RHEL/Fedora — NFS sharing
dnf install nfs-utils
systemctl enable --now nfs-server

# RHEL/Fedora — ACLs
dnf install acl nfs4-acl-tools

# Debian/Ubuntu — iSCSI targets
apt install targetcli-fb

# RHEL/Fedora — iSCSI targets
dnf install targetcli

# FreeBSD — iSCSI targets (ctld is built-in, just enable the service)
sysrc ctld_enable=YES
service ctld start

# FreeBSD — Windows network discovery
pkg install -g 'py3*-wsdd'

# ACME certificates via Let's Encrypt (lego) — only needed for --tls with ACME
apt install lego      # Debian/Ubuntu, or a release binary from https://github.com/go-acme/lego/releases
dnf install lego      # RHEL/Fedora
pkg install lego      # FreeBSD
```

After installing Samba, click **Initialize Samba** in the SMB section of the Users & Groups tab. dumpstore backs up any existing config (`smb.conf` / `smb4.conf`) to `<file>.pre-dumpstore` and renders its own; SMB shares, home shares, and Time Machine targets are disabled until then. If wsdd is installed later, use **Manage** in the Windows Discovery section so its workgroup follows Samba's.

---

## Install script (recommended)

Clone the repository and run `install.sh` as root. It is a thin wrapper around `make install`, which installs Go and Ansible if absent, builds the binary, installs everything to `/usr/local/lib/dumpstore/`, **prompts for an admin password**, and registers the service.

```bash
git clone https://github.com/langerma/dumpstore.git
cd dumpstore
sudo ./install.sh
```

To remove dumpstore completely:

```bash
sudo ./install.sh --uninstall
```

---

## Using make

```bash
# Optional: tag a release (omitting gives "dev" as version)
git tag v0.1.0

make build
sudo make install
```

`make install` detects the OS automatically and registers the appropriate service. The service will be available at `http://localhost:8080`.

---

## Authentication

dumpstore uses session-based login. The password is stored as an argon2id hash (PHC string format) in `/etc/dumpstore/dumpstore.conf` (Linux) or `/usr/local/etc/dumpstore/dumpstore.conf` (FreeBSD).

### Set or reset the password

```bash
# Linux
sudo /usr/local/lib/dumpstore/dumpstore --set-password --config /etc/dumpstore/dumpstore.conf
sudo systemctl restart dumpstore
# FreeBSD
sudo /usr/local/lib/dumpstore/dumpstore --set-password --config /usr/local/etc/dumpstore/dumpstore.conf
sudo service dumpstore restart
```

The prompt reads from `/dev/tty` so it works correctly even when stdin is piped.

### No password configured

If the config file is missing or has no `password_hash`, the service starts but **binds to `127.0.0.1:8080` only** and logs a warning. Run `--set-password` and restart to enable public binding.

### Config file reference

`/etc/dumpstore/dumpstore.conf` (Linux) / `/usr/local/etc/dumpstore/dumpstore.conf` (FreeBSD), mode `0600`:

```json
{
  "username": "admin",
  "password_hash": "$argon2id$v=19$m=65536,t=3,p=4$...",
  "session_ttl": "24h",
  "trusted_proxies": [],
  "unprotected_paths": ["/metrics"]
}
```

| Field | Default | Description |
|---|---|---|
| `username` | `admin` | Login username |
| `password_hash` | — | argon2id hash; set via `--set-password` (bcrypt hashes from before v0.1.13 are rejected — reset them) |
| `session_ttl` | `24h` | How long a session cookie stays valid |
| `trusted_proxies` | `[]` | CIDRs from which `X-Remote-User` header is trusted |
| `unprotected_paths` | `["/metrics"]` | Paths that bypass auth (prefix match) |

### Reverse proxy delegation

For setups behind nginx, Caddy, Traefik, or Authelia:

1. Add the proxy's CIDR to `trusted_proxies`
2. Configure your proxy to set `X-Remote-User: <username>` after SSO authentication
3. dumpstore will accept that header as the authenticated identity — no password login required from those IPs

### Change password or username in the UI

Go to **Users & Groups → Authentication** and use the Change Password or Change Username dialogs. Both operations go through Ansible and show the operation log.

---

## Run without installing

```bash
go build -o dumpstore .
sudo ./dumpstore -addr :8080 -dir .
```

`-dir` must point to the directory that contains `playbooks/` and `static/`. It defaults to the directory of the executable.

---

## Service configuration

### Linux (systemd)

The unit file is installed to `/etc/systemd/system/dumpstore.service`.

To change the listen address, edit `ExecStart` in the unit file:

```bash
sudo systemctl edit dumpstore
# add:
# [Service]
# ExecStart=
# ExecStart=/usr/local/lib/dumpstore/dumpstore -addr :9090
sudo systemctl daemon-reload && sudo systemctl restart dumpstore
```

### FreeBSD (rc.d)

The rc script is installed to `/usr/local/etc/rc.d/dumpstore`. To customise, add to `/etc/rc.conf`:

```
dumpstore_enable="YES"
dumpstore_addr=":9090"
dumpstore_dir="/usr/local/lib/dumpstore"
# optional: OpenTelemetry export (no-op when unset)
dumpstore_env="OTEL_EXPORTER_OTLP_ENDPOINT=http://collector.example:4318"
```

Then restart: `service dumpstore restart`. Logs go to `/var/log/dumpstore.log` in the same format as previous releases; with OTLP enabled they are additionally exported with `trace_id` correlation.

---

## Observability

Both exports integrate with an external stack — dumpstore never embeds dashboards or alerting.

**Prometheus (pull)** — `GET /metrics` exposes Go runtime and process stats, HTTP request counters and latency histograms (`http_requests_total`, `http_request_duration_seconds`), write-lane metrics (`ansible_runs_total`, `ansible_run_duration_seconds`, `dumpstore_op_runs_total`, `dumpstore_op_duration_seconds`), and `dumpstore_build_info`. Always on; `/metrics` bypasses login by default (`unprotected_paths`).

**OpenTelemetry (push)** — set the standard OTLP environment variables and dumpstore exports traces, logs, and runtime metrics to your collector (Alloy, Tempo/Loki/Mimir, Jaeger, …). Without them every OTEL code path is a no-op.

```bash
# Linux (systemd): systemctl edit dumpstore, or uncomment the examples in the unit
Environment=OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.example:4318
Environment=OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf   # default; "grpc" also supported
Environment=OTEL_SERVICE_NAME=dumpstore                 # default

# FreeBSD (rc.d): rc.subr passes ${name}_env to the daemon
sysrc dumpstore_env="OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.example:4318"
service dumpstore restart
```

When enabled you get a trace per API request (with child spans for every Ansible playbook and ops command, and root spans for jobs, scheduled replication, and auto-snapshot runs), the full log stream with `trace_id`/`span_id` correlation, and Go runtime + `http.server.*` metrics. All standard OTEL SDK env vars are honoured (`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_TRACES_SAMPLER`, `OTEL_SDK_DISABLED`, …). If the collector is down, OTLP records buffer and drop in the background — local logs are never affected.

Local logs are unchanged by OTEL: on Linux they go to the systemd journal (with syslog priority prefixes), on FreeBSD to `/var/log/dumpstore.log`. Every line for a request carries its `req_id` (taken from an upstream `X-Request-ID` header if present, and echoed back on the response), and every mutating operation emits an audit line with `remote_ip`, `action`, `target`, and `outcome`. For OTLP-only setups, `-log-stdout=false` turns local logging off; with no OTLP endpoint configured that would discard all logs, so dumpstore warns at startup.

See [[Architecture]] → Observability for how the pipeline is built.

---

## Uninstall

```bash
sudo make uninstall
# or
sudo ./install.sh --uninstall
```

---

## Versioning

Releases are tagged with semver (`v0.1.0`, `v0.2.0`, …). The version is injected at build time via ldflags:

```
v0.1.0                 ← exact tag
v0.1.0-3-gabcdef       ← 3 commits after tag
v0.1.0-3-gabcdef-dirty ← uncommitted changes present
dev                    ← built outside git
```

The version appears in:
- `./dumpstore -version`
- `GET /api/sysinfo` → `app_version` field
- `GET /metrics` → `dumpstore_build_info{version="..."}` label
- UI version badge
