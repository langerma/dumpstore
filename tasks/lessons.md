# Lessons

## 1. Always stay compatible with FreeBSD

**Rule:** This project targets both Linux and FreeBSD. Any code touching the OS layer must work on both platforms.

Checklist:
- Go: use `runtime.GOOS` guards (`"freebsd"` vs `"linux"`) for platform-specific paths, commands, and config files. Never assume Linux-only paths like `/etc/default/zfs` without a FreeBSD fallback.
- Shell/Ansible: avoid Linux-only tools (`systemctl`, `useradd`, `groupadd`) without FreeBSD equivalents (`service`, `pw useradd`, `pw groupadd`). Use `ansible_os_family` / `ansible_system` vars when needed.
- File paths: `/etc/periodic.conf` (FreeBSD) vs `/etc/default/zfs` (Linux), `/etc/login.defs` may not exist on FreeBSD — guard with `errors.Is(err, os.ErrNotExist)`.
- When adding a new write operation, check if the playbook command differs on FreeBSD and add a `when: ansible_system == "FreeBSD"` / `"Linux"` split if so.

## 2. Always update docs, wiki, and homepage when features change

**Rule:** After every feature addition or change, update ALL five of these before considering the task done:
1. **`README.md`** — feature list, planned table (mark done with strikethrough), API route table
2. **`FEATURES.md`** — detailed feature reference (if the file exists)
3. **`wiki/Home.md`** — features bullet list
4. **`wiki/API-Reference.md`** — quick-reference table + detailed endpoint section
5. **`docs/index.html`** — features card grid (add a card for each new feature)

Never leave these out of sync. If a feature moves from planned → done, strike it through in README and add the card to docs/index.html.

## 3. Verify top-level JS wiring against the HTML before committing

**Mistake:** Added `document.getElementById('scrubScheduleFreq').addEventListener(...)` at the top level of `app.js` referencing an element that was removed from `index.html`, and calling `updateScrubScheduleRows` which was never defined. This crashed the entire script on load, leaving the UI stuck on "Loading".

**Rule:** After writing any top-level `document.getElementById(...).addEventListener(...)` wiring in `app.js`, confirm:
1. The element ID exists in `index.html`.
2. The callback function is defined somewhere in `app.js`.

A quick `grep -n '<id>' static/index.html` and `grep -n 'function <cb>' static/app.js` before finishing is enough to catch this class of bug.
