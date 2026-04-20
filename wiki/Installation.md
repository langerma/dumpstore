# Installation

## Requirements

|                        | Linux                                                     | FreeBSD                                      |
|------------------------|-----------------------------------------------------------|----------------------------------------------|
| ZFS                    | `zfsutils-linux` or equivalent                            | built-in (`zfsutils` pkg for older releases) |
| Ansible                | `ansible` package (Python 3)                              | `py311-ansible` or equivalent                |
| Service manager        | systemd                                                   | rc.d (via `daemon(8)`)                       |
| S.M.A.R.T. (optional)  | `smartmontools`                                           | `smartmontools` pkg                          |
| POSIX ACLs (optional)  | `acl` pkg (`getfacl`/`setfacl`)                           | `py311-pylibacl` or `acl` port               |
| NFS sharing (optional) | `nfs-kernel-server` (Debian) or `nfs-utils` (RHEL/Fedora) | built-in base system                         |
| SMB sharing (optional) | `samba` (`smbd`, `net`, `pdbedit`)                        | `samba` pkg                                  |
| NFSv4 ACLs (optional)  | `nfs4-acl-tools` pkg                                      | `nfs4-acl-tools` port                        |
| iSCSI (optional)       | `targetcli-fb` (`targetcli`)                               | built-in `ctld`                              |
| Build                  | Go 1.22+                                                  | Go 1.22+                                     |

Go and Ansible are the only hard requirements. ZFS must be available on the target machine.

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

# Debian/Ubuntu — SMB sharing
apt install samba

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
```

After installing Samba, run **Configure Samba** from the dumpstore UI (Users & Groups → Configure Samba) or manually:

```bash
ansible-playbook playbooks/smb_setup.yml
```

---

## Install script (recommended)

Clone the repository and run `install.sh` as root. It is a thin wrapper around `make install`, which auto-installs Go and Ansible if absent, builds the binary, installs everything to `/usr/local/lib/dumpstore/`, **prompts for an admin password**, and registers the service.

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

dumpstore uses session-based login. The password is stored as a bcrypt hash in `/etc/dumpstore/dumpstore.conf` (Linux) or `/usr/local/etc/dumpstore/dumpstore.conf` (FreeBSD).

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
  "password_hash": "$2a$12$...",
  "session_ttl": "24h",
  "trusted_proxies": [],
  "unprotected_paths": ["/metrics"]
}
```

| Field | Default | Description |
|---|---|---|
| `username` | `admin` | Login username |
| `password_hash` | — | bcrypt hash (cost 12); set via `--set-password` |
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
```

Then restart: `service dumpstore restart`

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
