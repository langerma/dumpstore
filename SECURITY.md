# Security Notes

dumpstore is designed for **trusted, private networks** (home lab, local LAN). It has built-in session-based authentication and runs as root. The notes below describe known risks and the recommended mitigations.

---

## Authentication

dumpstore requires a password to access the UI and API. The password is bcrypt-hashed (cost 12) and stored in `/etc/dumpstore/dumpstore.conf` (mode `0600`). Session tokens are 32 bytes from `crypto/rand` (256-bit entropy), stored in memory, and expire after a configurable TTL (default 24 h).

Login attempts are rate-limited to 10 per IP per 60 seconds.

**Set or reset the password:**

```bash
sudo /usr/local/lib/dumpstore/dumpstore --set-password --config /etc/dumpstore/dumpstore.conf
sudo systemctl restart dumpstore
```

If no password is configured the service binds to `127.0.0.1` only and logs a warning.

---

## TLS / plaintext credentials

dumpstore does **not** enforce HTTPS at the application layer. The session cookie and several API endpoints accept passwords in the request body:

- `POST /auth/login` — admin password
- `POST /api/users` — Unix user password
- `POST /api/users/{name}` — Unix user password change
- `POST /api/smb/users` — Samba password

Without TLS these credentials travel in plaintext.

**Recommended mitigations (pick one):**

1. **Reverse proxy with TLS termination** — put dumpstore behind nginx, Caddy, or Traefik and only expose the proxy over HTTPS. The proxy handles certificate management; dumpstore stays on `127.0.0.1`.
2. **SSH tunnel** — `ssh -L 8080:localhost:8080 nas-host` and access via `http://localhost:8080`. No certificate required.
3. **VPN** — restrict access to a WireGuard or OpenVPN network you already trust.

A future release may add a built-in TLS flag (`-tls-cert` / `-tls-key`).

---

## Reverse proxy delegation

If you run dumpstore behind an authenticating proxy (nginx, Caddy, Authelia, etc.) you can configure the proxy's CIDR in `trusted_proxies` and have the proxy set `X-Remote-User`. dumpstore will accept that header as the authenticated identity from those IPs, bypassing the password login.

**Never** configure `trusted_proxies` to include IPs you do not fully control — any host in that range can impersonate any username.

---

## General advice

- Use a reverse proxy with TLS so the session cookie and passwords are encrypted in transit.
- The service runs as root (required for ZFS). Treat it with the same access controls you would apply to a root shell.
- Firewall the port at the OS level if a proxy is not in use.
