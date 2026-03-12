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
```

After installing Samba, run **Configure Samba** from the dumpstore UI (Users & Groups → Configure Samba) or manually:

```bash
ansible-playbook playbooks/smb_setup.yml
```

---

## Install script (recommended)

Clone the repository and run `install.sh` as root. It checks prerequisites, builds the binary, installs everything to `/usr/local/lib/dumpstore/`, and registers the service.

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
