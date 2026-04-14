BINARY  := dumpstore
INSTALL := /usr/local/lib/dumpstore
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build clean dev install uninstall check-prereqs \
        vm-linux-start vm-linux-stop vm-linux-ssh vm-linux-deploy vm-linux-destroy \
        vm-freebsd-start vm-freebsd-stop vm-freebsd-ssh vm-freebsd-deploy vm-freebsd-destroy

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

# ---------------------------------------------------------------------------
# Dev VMs (Lima — requires: brew install lima)
# Linux UI:  http://localhost:8080
# FreeBSD UI: http://localhost:8081
# ---------------------------------------------------------------------------

VM_LINUX   := dumpstore-linux
VM_FREEBSD := dumpstore-freebsd

# Build a linux/arm64 binary for deployment into the Linux VM
build-linux:
	GOOS=linux GOARCH=arm64 go build -buildvcs=false \
	  -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY)-linux .

# Build a freebsd/arm64 binary for deployment into the FreeBSD VM
build-freebsd:
	GOOS=freebsd GOARCH=arm64 go build -buildvcs=false \
	  -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY)-freebsd .

vm-linux-start:
	@command -v limactl >/dev/null 2>&1 || { \
	  echo "error: limactl not found. Install Lima via:" >&2; \
	  echo "  brew:      brew install lima" >&2; \
	  echo "  MacPorts:  sudo port install lima" >&2; \
	  echo "  manual:    https://github.com/lima-vm/lima/releases" >&2; \
	  exit 1; }
	@if limactl list -q | grep -qx "$(VM_LINUX)"; then \
	  echo "==> Starting existing VM $(VM_LINUX)..."; \
	  limactl start $(VM_LINUX); \
	else \
	  echo "==> Creating and provisioning VM $(VM_LINUX) (first run, takes a few minutes)..."; \
	  limactl create --name=$(VM_LINUX) dev/lima-linux.yaml; \
	  limactl start $(VM_LINUX); \
	fi
	@echo "==> Linux VM ready. UI will be at http://localhost:8080 after deploy."
	@echo "    Run: make vm-linux-deploy"

vm-linux-stop:
	limactl stop $(VM_LINUX)

vm-linux-ssh:
	limactl shell $(VM_LINUX)

vm-linux-deploy: build-linux
	@echo "==> Deploying to $(VM_LINUX)..."
	limactl copy $(BINARY)-linux $(VM_LINUX):/tmp/dumpstore
	limactl copy -r playbooks   $(VM_LINUX):/tmp/playbooks
	limactl copy -r static      $(VM_LINUX):/tmp/static
	@rm -f $(BINARY)-linux
	limactl shell --tty=false $(VM_LINUX) -- sudo sh -c '\
	  install -d /usr/local/lib/dumpstore && \
	  install -m 0755 /tmp/dumpstore /usr/local/lib/dumpstore/dumpstore && \
	  rm -rf /usr/local/lib/dumpstore/playbooks /usr/local/lib/dumpstore/static && \
	  cp -r /tmp/playbooks /usr/local/lib/dumpstore/ && \
	  cp -r /tmp/static    /usr/local/lib/dumpstore/ && \
	  install -d -m 0700 /etc/dumpstore && \
	  if ! grep -q password_hash /etc/dumpstore/dumpstore.conf 2>/dev/null; then \
	    /usr/local/lib/dumpstore/dumpstore --set-password --config /etc/dumpstore/dumpstore.conf; \
	  fi && \
	  systemctl restart dumpstore 2>/dev/null || /usr/local/lib/dumpstore/dumpstore \
	    -addr :8080 -dir /usr/local/lib/dumpstore -config /etc/dumpstore/dumpstore.conf &'
	@echo "==> Deployed. Open http://localhost:8080"

vm-linux-destroy:
	limactl delete --force $(VM_LINUX)

vm-freebsd-start:
	@command -v limactl >/dev/null 2>&1 || { \
	  echo "error: limactl not found. Install Lima via:" >&2; \
	  echo "  brew:      brew install lima" >&2; \
	  echo "  MacPorts:  sudo port install lima" >&2; \
	  echo "  manual:    https://github.com/lima-vm/lima/releases" >&2; \
	  exit 1; }
	@if limactl list -q | grep -qx "$(VM_FREEBSD)"; then \
	  echo "==> Starting existing VM $(VM_FREEBSD)..."; \
	  limactl start $(VM_FREEBSD); \
	else \
	  echo "==> Creating and provisioning VM $(VM_FREEBSD) (first run, takes a few minutes)..."; \
	  limactl create --name=$(VM_FREEBSD) dev/lima-freebsd.yaml; \
	  limactl start $(VM_FREEBSD); \
	fi
	@echo "==> FreeBSD VM ready. UI will be at http://localhost:8081 after deploy."
	@echo "    Run: make vm-freebsd-deploy"

vm-freebsd-stop:
	limactl stop $(VM_FREEBSD)

vm-freebsd-ssh:
	limactl shell $(VM_FREEBSD)

vm-freebsd-deploy: build-freebsd
	@echo "==> Deploying to $(VM_FREEBSD)..."
	limactl copy $(BINARY)-freebsd $(VM_FREEBSD):/tmp/dumpstore
	limactl copy -r playbooks      $(VM_FREEBSD):/tmp/playbooks
	limactl copy -r static         $(VM_FREEBSD):/tmp/static
	@rm -f $(BINARY)-freebsd
	limactl shell --tty=false $(VM_FREEBSD) -- sudo sh -c '\
	  install -d /usr/local/lib/dumpstore && \
	  install -m 0755 /tmp/dumpstore /usr/local/lib/dumpstore/dumpstore && \
	  rm -rf /usr/local/lib/dumpstore/playbooks /usr/local/lib/dumpstore/static && \
	  cp -r /tmp/playbooks /usr/local/lib/dumpstore/ && \
	  cp -r /tmp/static    /usr/local/lib/dumpstore/ && \
	  install -d -m 0700 /usr/local/etc/dumpstore && \
	  if ! grep -q password_hash /usr/local/etc/dumpstore/dumpstore.conf 2>/dev/null; then \
	    /usr/local/lib/dumpstore/dumpstore --set-password --config /usr/local/etc/dumpstore/dumpstore.conf; \
	  fi && \
	  service dumpstore restart 2>/dev/null || /usr/local/lib/dumpstore/dumpstore \
	    -addr :8080 -dir /usr/local/lib/dumpstore -config /usr/local/etc/dumpstore/dumpstore.conf &'
	@echo "==> Deployed. Open http://localhost:8081"

vm-freebsd-destroy:
	limactl delete --force $(VM_FREEBSD)

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
