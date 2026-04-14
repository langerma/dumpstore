BINARY  := dumpstore
INSTALL := /usr/local/lib/dumpstore
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build clean dev install uninstall check-prereqs

all: build

build:
	go build -buildvcs=false -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .

# Run locally on macOS (or any machine without ZFS/Ansible).
# Fake CLI stubs in dev/bin/ intercept zfs, zpool, and ansible-playbook.
dev:
	chmod +x dev/bin/*
	PATH="$(CURDIR)/dev/bin:$$PATH" go run . -dir $(CURDIR) -debug

clean:
	rm -f $(BINARY)

check-prereqs:
	@command -v go               >/dev/null 2>&1 || { echo "error: 'go' not found in PATH — please install Go first" >&2; exit 1; }
	@command -v ansible-playbook >/dev/null 2>&1 || { echo "error: 'ansible-playbook' not found — please install Ansible first" >&2; exit 1; }
	@command -v lego             >/dev/null 2>&1 || echo "  [warn] lego not found — ACME cert issuance will not be available"

install: check-prereqs build
	@set -e; \
	OS=$$(uname -s); \
	case "$$OS" in \
	    FreeBSD) CONFIG_DIR=/usr/local/etc/dumpstore ;; \
	    *)       CONFIG_DIR=/etc/dumpstore ;; \
	esac; \
	echo "==> Installing to $(INSTALL)..."; \
	install -d $(INSTALL); \
	install -m 0755 $(BINARY) $(INSTALL)/$(BINARY); \
	rm -f $(BINARY); \
	rm -rf $(INSTALL)/playbooks $(INSTALL)/static; \
	cp -r playbooks $(INSTALL)/; \
	cp -r static    $(INSTALL)/; \
	echo "==> Configuring authentication..."; \
	install -d -m 0700 $$CONFIG_DIR; \
	if ! grep -q '"password_hash"' $$CONFIG_DIR/dumpstore.conf 2>/dev/null || \
	     grep -q '"password_hash": ""' $$CONFIG_DIR/dumpstore.conf 2>/dev/null; then \
	    echo "Set admin password (used to log in to the web UI):"; \
	    $(INSTALL)/$(BINARY) --set-password --config $$CONFIG_DIR/dumpstore.conf; \
	else \
	    echo "Password already configured, skipping."; \
	fi; \
	echo "==> Setting up service..."; \
	case "$$OS" in \
	    Linux) \
	        install -m 0644 contrib/dumpstore.service /etc/systemd/system/dumpstore.service; \
	        systemctl daemon-reload; \
	        systemctl enable --now dumpstore; \
	        echo "==> Done. dumpstore is running on http://localhost:8080"; \
	        echo "    Logs: journalctl -u dumpstore -f"; \
	        ;; \
	    FreeBSD) \
	        install -m 0555 contrib/dumpstore.rc /usr/local/etc/rc.d/dumpstore; \
	        sysrc dumpstore_enable=YES; \
	        service dumpstore restart 2>/dev/null || service dumpstore start; \
	        echo "==> Done. dumpstore is running on http://localhost:8080"; \
	        echo "    Logs: service dumpstore status"; \
	        ;; \
	    *) \
	        echo "Warning: unknown OS '$$OS' — binary installed but service not registered."; \
	        echo "  Start manually: $(INSTALL)/$(BINARY) -addr :8080 -dir $(INSTALL) -config $$CONFIG_DIR/dumpstore.conf"; \
	        ;; \
	esac

uninstall:
	@set -e; \
	OS=$$(uname -s); \
	echo "==> Stopping and removing dumpstore..."; \
	case "$$OS" in \
	    Linux) \
	        systemctl disable --now dumpstore 2>/dev/null || true; \
	        rm -f /etc/systemd/system/dumpstore.service; \
	        systemctl daemon-reload; \
	        ;; \
	    FreeBSD) \
	        service dumpstore stop 2>/dev/null || true; \
	        sysrc -x dumpstore_enable 2>/dev/null || true; \
	        rm -f /usr/local/etc/rc.d/dumpstore; \
	        ;; \
	esac; \
	rm -rf $(INSTALL); \
	echo "==> dumpstore uninstalled."
