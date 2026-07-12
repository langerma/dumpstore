BINARY  := dumpstore
INSTALL := /usr/local/lib/dumpstore
.PHONY: all build clean dev install uninstall check-prereqs install-go install-ansible \
        test-integration release \
        vm-linux-start vm-linux-stop vm-linux-ssh vm-linux-deploy vm-linux-destroy \
        vm-freebsd-start vm-freebsd-stop vm-freebsd-ssh vm-freebsd-deploy vm-freebsd-destroy

all: build

build:
	go build -buildvcs=false -ldflags="-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o $(BINARY) .

# Cross-build and package every release target into dist/.
# Single source of truth for the release recipe — used by release.yml on
# tags and by the CI release-smoke job on every PR (#118) so the two
# cannot drift.
# Usage: make release VERSION=v1.2.3
RELEASE_TARGETS := linux/amd64 linux/arm64 freebsd/amd64 freebsd/arm64

release:
	@test -n "$(VERSION)" || { echo "error: VERSION is required (make release VERSION=v1.2.3)" >&2; exit 1; }
	@rm -rf dist && mkdir -p dist
	@set -e; \
	for target in $(RELEASE_TARGETS); do \
	  OS=$${target%/*}; ARCH=$${target#*/}; \
	  NAME="dumpstore-$(VERSION)-$$OS-$$ARCH"; \
	  echo "==> Building $$NAME"; \
	  GOOS=$$OS GOARCH=$$ARCH go build -buildvcs=false \
	    -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .; \
	  mkdir -p dist/$$NAME; \
	  cp $(BINARY) dist/$$NAME/; \
	  cp -r playbooks static README.md install.sh dist/$$NAME/; \
	  case "$$OS" in \
	    linux)   cp contrib/dumpstore.service dist/$$NAME/ ;; \
	    freebsd) cp contrib/dumpstore.rc      dist/$$NAME/ ;; \
	  esac; \
	  tar -czf dist/$$NAME.tar.gz -C dist $$NAME; \
	  rm -rf dist/$$NAME $(BINARY); \
	done
	@ls -l dist/

# Run locally on macOS (or any machine without ZFS/Ansible).
# Fake CLI stubs in dev/bin/ intercept zfs, zpool, and ansible-playbook.
dev:
	chmod +x dev/bin/*
	PATH="$(CURDIR)/dev/bin:$$PATH" go run . -dir $(CURDIR) -debug

clean:
	rm -f $(BINARY)

install-go:
	@if command -v go >/dev/null 2>&1; then \
	  echo "==> Go already installed: $$(go version)"; \
	else \
	  echo "==> Installing Go..."; \
	  OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	  ARCH=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
	  if command -v curl >/dev/null 2>&1; then \
	    FETCH="curl -fsSL"; \
	  elif command -v fetch >/dev/null 2>&1; then \
	    FETCH="fetch -qo -"; \
	  else \
	    echo "error: neither curl nor fetch found" >&2; exit 1; \
	  fi; \
	  GOVERSION=$$($$FETCH "https://go.dev/VERSION?m=text" | head -1); \
	  $$FETCH "https://go.dev/dl/$${GOVERSION}.$${OS}-$${ARCH}.tar.gz" | tar -C /usr/local -xz; \
	  echo 'export PATH=$$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh 2>/dev/null || true; \
	  export PATH=$$PATH:/usr/local/go/bin; \
	  echo "==> Installed $$(go version)"; \
	fi

check-prereqs: install-go install-ansible
	@command -v lego >/dev/null 2>&1 || echo "  [warn] lego not found — ACME cert issuance will not be available"

install-ansible:
	@if command -v ansible-playbook >/dev/null 2>&1; then \
	  echo "==> Ansible already installed: $$(ansible-playbook --version | head -1)"; \
	else \
	  echo "==> Installing Ansible..."; \
	  OS=$$(uname -s); \
	  case "$$OS" in \
	    FreeBSD) pkg install -y sysutils/ansible ;; \
	    Linux)   apt-get install -y -qq ansible ;; \
	    *) echo "error: 'ansible-playbook' not found — please install Ansible" >&2; exit 1 ;; \
	  esac; \
	fi

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
	if ! grep -qF '"password_hash": "$$argon2id$$' $$CONFIG_DIR/dumpstore.conf 2>/dev/null; then \
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
	        systemctl enable dumpstore; \
	        systemctl restart dumpstore; \
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
	  echo "==> Creating ZFS data disk..."; \
	  limactl disk create dumpstore-linux-data --size 10GiB 2>/dev/null || true; \
	  echo "==> Creating integration-test scratch disks..."; \
	  for n in 1 2 3; do \
	    limactl disk create dumpstore-linux-test$$n --size 1GiB 2>/dev/null || true; \
	  done; \
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

vm-linux-deploy:
	@echo "==> Packing source..."
	COPYFILE_DISABLE=1 tar --no-xattrs -czf /tmp/dumpstore-src.tar.gz \
	  --exclude='.git' --exclude='dumpstore' --exclude='*.tar.gz' \
	  -C $(CURDIR) .
	@echo "==> Copying source to $(VM_LINUX)..."
	limactl copy /tmp/dumpstore-src.tar.gz $(VM_LINUX):/tmp/dumpstore-src.tar.gz
	@rm -f /tmp/dumpstore-src.tar.gz
	@echo "==> Running make install in VM..."
	limactl shell --tty=false $(VM_LINUX) -- sudo sh -c \
	  'rm -rf /tmp/dumpstore-src && mkdir /tmp/dumpstore-src && \
	   tar -xzf /tmp/dumpstore-src.tar.gz -C /tmp/dumpstore-src && \
	   cd /tmp/dumpstore-src && \
	   PATH=/usr/local/go/bin:$$PATH make install'
	@echo "==> Deployed. Open http://localhost:8080  (admin / admin)"

vm-linux-destroy:
	limactl delete --force $(VM_LINUX) || true
	limactl disk delete dumpstore-linux-data 2>/dev/null || true
	@for n in 1 2 3; do \
	  limactl disk delete dumpstore-linux-test$$n 2>/dev/null || true; \
	done

# Integration tests — drive the deployed dumpstore API in the Lima VM.
# Prerequisites: make vm-linux-start && make vm-linux-deploy
# See tests/integration/README.md for env overrides (FreeBSD VM, disks, …).
test-integration:
	go test -tags integration -count=1 -timeout 30m -v ./tests/integration/...

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
	  echo "==> Creating ZFS data disk..."; \
	  limactl disk create dumpstore-freebsd-data --size 10GiB 2>/dev/null || true; \
	  echo "==> Creating integration-test scratch disks..."; \
	  for n in 1 2 3; do \
	    limactl disk create dumpstore-freebsd-test$$n --size 1GiB 2>/dev/null || true; \
	  done; \
	  echo "==> Creating and provisioning VM $(VM_FREEBSD) (first run, takes a few minutes)..."; \
	  limactl create --name=$(VM_FREEBSD) dev/lima-freebsd.yaml; \
	  limactl start $(VM_FREEBSD); \
	fi
	@echo "==> Setting up port forwarding (localhost:8081 -> VM:8080)..."
	@ssh -F $(HOME)/.lima/$(VM_FREEBSD)/ssh.config \
	  -L 8081:127.0.0.1:8080 -N -f -o ExitOnForwardFailure=yes \
	  lima-$(VM_FREEBSD) 2>/dev/null || true
	@echo "==> FreeBSD VM ready. UI will be at http://localhost:8081 after deploy."
	@echo "    Run: make vm-freebsd-deploy"

vm-freebsd-stop:
	@pkill -f "ssh.*8081:127.0.0.1:8080" 2>/dev/null || true
	limactl stop $(VM_FREEBSD)

vm-freebsd-ssh:
	limactl shell $(VM_FREEBSD)

vm-freebsd-deploy:
	@echo "==> Packing source..."
	COPYFILE_DISABLE=1 tar --no-xattrs -czf /tmp/dumpstore-src.tar.gz \
	  --exclude='.git' --exclude='dumpstore' --exclude='*.tar.gz' \
	  -C $(CURDIR) .
	@echo "==> Copying source to $(VM_FREEBSD)..."
	limactl copy /tmp/dumpstore-src.tar.gz $(VM_FREEBSD):/tmp/dumpstore-src.tar.gz
	@rm -f /tmp/dumpstore-src.tar.gz
	@echo "==> Running make install in VM..."
	limactl shell --tty=false $(VM_FREEBSD) -- sudo sh -c \
	  'rm -rf /tmp/dumpstore-src && mkdir /tmp/dumpstore-src && \
	   tar --no-xattrs -xzf /tmp/dumpstore-src.tar.gz -C /tmp/dumpstore-src && \
	   cd /tmp/dumpstore-src && \
	   PATH=/usr/local/go/bin:/usr/local/bin:$$PATH make install'
	@echo "==> Deployed. Open http://localhost:8081  (admin / admin)"

vm-freebsd-destroy:
	limactl delete --force $(VM_FREEBSD) || true
	limactl disk delete dumpstore-freebsd-data 2>/dev/null || true
	@for n in 1 2 3; do \
	  limactl disk delete dumpstore-freebsd-test$$n 2>/dev/null || true; \
	done

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
