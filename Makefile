BINARY   := dumpstore
INSTALL  := /usr/local/lib/dumpstore
OS       := $(shell uname -s)

# Linux service paths
SYSTEMD_SERVICE := /etc/systemd/system/dumpstore.service

# FreeBSD service path
RC_SERVICE := /usr/local/etc/rc.d/dumpstore

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build clean dev install uninstall

all: build

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .

# Run locally on macOS (or any machine without ZFS/Ansible).
# Fake CLI stubs in dev/bin/ intercept zfs, zpool, and ansible-playbook.
dev:
	chmod +x dev/bin/*
	PATH="$(CURDIR)/dev/bin:$$PATH" go run . -dir $(CURDIR) -debug

clean:
	rm -f $(BINARY)

install: build
	@echo "Installing dumpstore to $(INSTALL)..."
	install -d $(INSTALL)
	install -m 0755 $(BINARY) $(INSTALL)/$(BINARY)
	cp -r playbooks $(INSTALL)/
	cp -r static    $(INSTALL)/
	@echo "Configuring authentication..."
	install -d -m 0700 /etc/dumpstore
	@if ! grep -q '"password_hash"' /etc/dumpstore/dumpstore.conf 2>/dev/null || grep -q '"password_hash": ""' /etc/dumpstore/dumpstore.conf 2>/dev/null; then \
	    echo "Set admin password (used to log in to the web UI):"; \
	    $(INSTALL)/$(BINARY) --set-password --config /etc/dumpstore/dumpstore.conf; \
	else \
	    echo "Password already configured, skipping."; \
	fi
ifeq ($(OS),Linux)
	install -m 0644 contrib/dumpstore.service $(SYSTEMD_SERVICE)
	systemctl daemon-reload
	systemctl enable --now dumpstore
else ifeq ($(OS),FreeBSD)
	install -m 0555 contrib/dumpstore.rc $(RC_SERVICE)
	sysrc dumpstore_enable=YES
	service dumpstore start
else
	@echo "Warning: unknown OS '$(OS)' — binary installed but service not registered."
	@echo "  Start manually: sudo $(INSTALL)/$(BINARY) -addr :8080 -dir $(INSTALL) -config /etc/dumpstore/dumpstore.conf"
endif
	@echo "Done. Service running on http://localhost:8080"

uninstall:
ifeq ($(OS),Linux)
	systemctl disable --now dumpstore || true
	rm -f $(SYSTEMD_SERVICE)
	systemctl daemon-reload
else ifeq ($(OS),FreeBSD)
	service dumpstore stop || true
	sysrc -x dumpstore_enable || true
	rm -f $(RC_SERVICE)
endif
	rm -rf $(INSTALL)
	@echo "dumpstore uninstalled."
