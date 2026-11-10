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

# FreeBSD — iSCSI targets: ctld is built-in. Nothing to install; the iSCSI
# dialog's "Initialize ctld" (POST /api/iscsi/init) takes over /etc/ctl.conf
# (backup: /etc/ctl.conf.pre-dumpstore), enables ctld, and starts it.

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
| `tls_enabled` | `false` | Set to `true` by every TLS action in the UI; informational only — HTTPS starts only with `--tls` (see [TLS / HTTPS](#tls--https)) |
| `tls_cert_path` / `tls_key_path` | — | PEM certificate and key served with `--tls` |
| `acme_enabled` / `acme_email` / `acme_domain` | — | Read at startup, but currently **not written** by *Let's Encrypt (ACME)* — the UI keeps them in memory only, so *Renew (ACME)* stops working after a restart |

### Reverse proxy delegation

For setups behind nginx, Caddy, Traefik, or Authelia:

1. Add the proxy's CIDR to `trusted_proxies`
2. Configure your proxy to set `X-Remote-User: <username>` after SSO authentication
3. dumpstore will accept that header as the authenticated identity — no password login required from those IPs

### Change password or username in the UI

Go to **Users & Groups → Authentication** and use the Change Password or Change Username dialogs. Both operations go through Ansible and show the operation log.

---

## TLS / HTTPS

dumpstore serves plain HTTP on `-addr` by default. HTTPS is a two-step setup: put a certificate in place, then restart dumpstore with `--tls`.

**1. Certificate** — **Users & Groups → TLS / HTTPS** offers three ways; each writes the paths into `dumpstore.conf`:

| Action | What it does |
|---|---|
| *Generate self-signed* | ECDSA P-256 certificate valid 10 years for the given hostname (CN + DNS SAN), written as `cert.pem` / `key.pem` (key `0600`) to `<config dir>/tls` by default |
| *Load existing cert* | Uses a certificate and key already on disk (e.g. from your own CA or certbot) |
| *Let's Encrypt (ACME)* | Runs [`lego`](https://go-acme.github.io/lego/) with the **TLS-ALPN-01** challenge: Let's Encrypt must reach this host on **port 443**, and nothing — including dumpstore itself — may be listening there while it runs. Certificates go to `<config dir>/tls/acme/certificates/<domain>.crt` / `.key` |

**2. Restart with `--tls`** — HTTPS starts only when the `--tls` flag is set *and* both cert paths are configured; otherwise dumpstore keeps serving HTTP on `-addr`. With `--tls`, `-addr` is ignored: HTTPS listens on `--tls-port` (default `443`) and a redirect server on `--http-port` (default `80`) answers every plain-HTTP request with `301` to `https://`.

```bash
# Linux: add --tls to ExecStart
sudo systemctl edit dumpstore
# [Service]
# ExecStart=
# ExecStart=/usr/local/lib/dumpstore/dumpstore --tls -dir /usr/local/lib/dumpstore -config /etc/dumpstore/dumpstore.conf
sudo systemctl daemon-reload && sudo systemctl restart dumpstore
```

On FreeBSD the shipped rc.d script has no variable for extra flags, so `--tls` cannot be set from `rc.conf` yet.

Things to know:

- **The certificate is loaded once at startup.** After replacing or renewing it, restart dumpstore.
- **ACME renewal is manual, and in practice has to happen outside dumpstore.** There is no renewal timer. The *Renew (ACME)* action (`POST /api/tls/acme/renew`, renews when fewer than 30 days remain) only works until dumpstore restarts — the ACME settings are not saved to `dumpstore.conf` — and HTTPS needs a restart. It also uses TLS-ALPN-01 on port 443, which fails while dumpstore itself serves HTTPS there. Renew the 90-day certificate with `lego renew` directly (e.g. from cron, with its HTTP-01 or DNS-01 challenge, `--path <config dir>/tls/acme`) and restart dumpstore afterwards. If ACME matters to you, a certificate managed by your reverse proxy or certbot plus *Load existing cert* is the more robust setup.
- Behind a reverse proxy, terminate TLS at the proxy instead and leave dumpstore on HTTP (see [Reverse proxy delegation](#reverse-proxy-delegation)).

---

## Run without installing

```bash
go build -o dumpstore .
sudo ./dumpstore -addr :8080 -dir .
```

`-dir` must point to the directory that contains `playbooks/` and `static/`. It defaults to the directory of the executable.

### Command-line flags

| Flag | Default | Description |
|---|---|---|
| `-addr` | `:8080` | Listen address for HTTP. Forced to `127.0.0.1:8080` while no password is configured; ignored with `--tls` |
| `-dir` | executable's directory | Base directory containing `playbooks/` and `static/` |
| `-config` | `/etc/dumpstore/dumpstore.conf` (Linux), `/usr/local/etc/dumpstore/dumpstore.conf` (FreeBSD) | Config file (auth, TLS) |
| `-set-password` | `false` | Prompt for a new admin password, write its hash to `-config`, and exit |
| `-tls` | `false` | Serve HTTPS with the configured certificate (see [TLS / HTTPS](#tls--https)) |
| `-tls-port` | `443` | HTTPS port when `--tls` is set |
| `-http-port` | `80` | Port of the HTTP → HTTPS redirect server when `--tls` is set |
| `-debug` | `false` | Log at debug level |
| `-log-stdout` | `true` | Write logs to stdout (journald / log file); set `false` only when shipping logs via OTLP (see [Observability](#observability)) |
| `-version` | — | Print the version and exit |

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

- **Prometheus** — `GET /metrics` is always on (and unauthenticated by default via `unprotected_paths`); point a scrape job at it.
- **OpenTelemetry** — set `OTEL_EXPORTER_OTLP_ENDPOINT` on the service to push traces, logs, and runtime metrics to an OTLP collector:

```bash
# Linux (systemd)
sudo systemctl edit dumpstore   # [Service] Environment=OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.example:4318
sudo systemctl restart dumpstore
# FreeBSD (rc.d)
sysrc dumpstore_env="OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.example:4318" && service dumpstore restart
```

- **Logs** — journald on Linux, `/var/log/dumpstore.log` on FreeBSD; every request line carries a `req_id`, every write an audit line.

The full metric list, all OTEL variables, span names, and the log/audit format are on the [[Observability]] page.

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
