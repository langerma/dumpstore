# Security Notes

dumpstore is designed for **trusted, private networks** (home lab, local LAN). It has built-in session-based authentication and runs as root. The notes below describe known risks and the recommended mitigations.

---

## Authentication

dumpstore requires a password to access the UI and API. The password is hashed with argon2id (3 passes, 64 MiB, 4 threads; PHC string format) and stored in `/etc/dumpstore/dumpstore.conf` (Linux) or `/usr/local/etc/dumpstore/dumpstore.conf` (FreeBSD) (mode `0600`). Session tokens are 32 bytes from `crypto/rand` (256-bit entropy), stored in memory, and expire after a configurable TTL (default 24 h).

Failed login attempts are rate-limited to 10 per IP per 60 seconds; a successful login resets the IP's counter. A blocked browser is redirected to the login page with an error, an API client (`Accept: application/json`) gets `429` with `Retry-After`.

**Set or reset the password:**

```bash
# Linux
sudo /usr/local/lib/dumpstore/dumpstore --set-password --config /etc/dumpstore/dumpstore.conf
sudo systemctl restart dumpstore

# FreeBSD
sudo /usr/local/lib/dumpstore/dumpstore --set-password --config /usr/local/etc/dumpstore/dumpstore.conf
sudo service dumpstore restart
```

If no password is configured the service binds to `127.0.0.1` only and logs a warning.

---

## TLS / plaintext credentials

dumpstore does **not** enforce HTTPS at the application layer. The session cookie and several API endpoints accept passwords in the request body:

- `POST /auth/login` — admin password
- `PUT /api/v1/auth/password` — admin password change
- `POST /api/v1/users` — Unix user password
- `PUT /api/v1/users/{name}` — Unix user password change
- `PUT /api/v1/smb/users/{name}` — Samba password
- `POST /api/v1/iscsi/targets` — iSCSI CHAP secret (`chap_password`)

Without TLS these credentials travel in plaintext.

**Recommended mitigations (pick one):**

1. **Reverse proxy with TLS termination** — put dumpstore behind nginx, Caddy, or Traefik and only expose the proxy over HTTPS. The proxy handles certificate management; dumpstore stays on `127.0.0.1`.
2. **SSH tunnel** — `ssh -L 8080:localhost:8080 nas-host` and access via `http://localhost:8080`. No certificate required.
3. **VPN** — restrict access to a WireGuard or OpenVPN network you already trust.
4. **Built-in TLS** — start dumpstore with `--tls` and set `tls_cert_path` / `tls_key_path` in the config file (self-signed generation, an existing certificate, or ACME via `lego` from the UI). HTTPS listens on `--tls-port` (443) and `--http-port` (80) redirects to it. The certificate is loaded at startup, so restart after changing it. See the wiki's Installation page.

---

## Values passed to Ansible

Configuration writes run `ansible-playbook` as root. Their values (passwords included) are written to a temporary vars file readable only by root (mode `0600`, removed when the playbook finishes) and passed as `--extra-vars @<file>`, so they don't appear in process arguments that other local users can read with `ps`. The file is tagged `!unsafe`, so Ansible never evaluates `{{ … }}` or `{% … %}` in a value (#214).

Known exception: on Linux, creating an iSCSI target with CHAP runs `targetcli … set auth password=<secret>`, which puts the CHAP secret on that command's argv while it runs (#218).

---

## Reverse proxy delegation

If you run dumpstore behind an authenticating proxy (nginx, Caddy, Authelia, etc.) you can configure the proxy's CIDR in `trusted_proxies` and have the proxy set `X-Remote-User`. dumpstore will accept that header as the authenticated identity from those IPs, bypassing the password login.

**Never** configure `trusted_proxies` to include IPs you do not fully control — any host in that range can impersonate any username.

**Same-host proxy caveat:** with the proxy on the dumpstore host, `trusted_proxies` has to contain `127.0.0.1/32`, and then **every local user or process** that can reach `127.0.0.1:8080` can send `X-Remote-User` and use the full API as root. Only do this on hosts without untrusted local users, or run the proxy on another host. Trust scoped to a permission-restricted unix socket is planned in [#212](https://github.com/langerma/dumpstore/issues/212).

---

## General advice

- Use a reverse proxy with TLS so the session cookie and passwords are encrypted in transit.
- The service runs as root (required for ZFS). Treat it with the same access controls you would apply to a root shell.
- Firewall the port at the OS level if a proxy is not in use.
