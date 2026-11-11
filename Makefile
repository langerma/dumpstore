BINARY  := dumpstore
INSTALL := /usr/local/lib/dumpstore

# install-go puts Go in /usr/local/go. Every recipe runs in its own shell, so
# an `export PATH` inside install-go never reached `build`: on a host without
# Go, `make install` installed it and then failed with "go: not found".
export PATH := $(PATH):/usr/local/go/bin
.PHONY: all build clean dev install uninstall check-prereqs install-go install-ansible \
        test-integration screenshots release openapi \
        ci ci-full ci-go ci-frontend ci-openapi-lint ci-forge ci-release ci-docs \
        ci-integration ci-freebsd vm-linux-verify vm-freebsd-verify forge-rev \
        vm-linux-start vm-linux-stop vm-linux-ssh vm-linux-deploy vm-linux-destroy \
        vm-freebsd-start vm-freebsd-stop vm-freebsd-ssh vm-freebsd-deploy vm-freebsd-destroy

all: build

build:
	go build -buildvcs=false -ldflags="-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o $(BINARY) .

# Regenerate api/openapi.json from the route registry (#181). CI fails
# (TestOpenAPISpec) when the committed file is stale.
openapi:
	go test ./internal/api/ -run '^TestOpenAPISpec$$' -update

# Cross-build and package every release target into $(DIST) (default dist/).
# Single source of truth for the release recipe — used by release.yml on
# tags and by the CI release-smoke job on every PR (#118) so the two
# cannot drift.
# Usage: make release VERSION=v1.2.3 [DIST=dir]
RELEASE_TARGETS := linux/amd64 linux/arm64 freebsd/amd64 freebsd/arm64
DIST ?= dist

release:
	@test -n "$(VERSION)" || { echo "error: VERSION is required (make release VERSION=v1.2.3)" >&2; exit 1; }
	@case "$(DIST)" in ""|/*|*..*) echo "error: DIST must be a relative directory inside the tree" >&2; exit 1 ;; esac
	@rm -rf "$(DIST)" && mkdir -p "$(DIST)"
	@set -e; \
	for target in $(RELEASE_TARGETS); do \
	  OS=$${target%/*}; ARCH=$${target#*/}; \
	  NAME="dumpstore-$(VERSION)-$$OS-$$ARCH"; \
	  echo "==> Building $$NAME"; \
	  GOOS=$$OS GOARCH=$$ARCH go build -buildvcs=false \
	    -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .; \
	  mkdir -p "$(DIST)/$$NAME"; \
	  cp $(BINARY) "$(DIST)/$$NAME/"; \
	  cp -r playbooks static README.md install.sh "$(DIST)/$$NAME/"; \
	  case "$$OS" in \
	    linux)   cp contrib/dumpstore.service "$(DIST)/$$NAME/" ;; \
	    freebsd) cp contrib/dumpstore.rc      "$(DIST)/$$NAME/" ;; \
	  esac; \
	  tar -czf "$(DIST)/$$NAME.tar.gz" -C "$(DIST)" "$$NAME"; \
	  rm -rf "$(DIST)/$$NAME" $(BINARY); \
	done
	@ls -l "$(DIST)/"

# ---------------------------------------------------------------------------
# CI — every job in .github/workflows/ci.yml and check-docs.yml is one target
# here; the workflows only set up toolchains and call these, so a local run
# and CI cannot drift (same pattern as `release`).
#
#   make ci          all PR checks, in parallel, with a pass/fail table
#   make ci-full     + the Linux and FreeBSD VM suites, in parallel
#   make ci-integration / ci-freebsd   one VM suite (the linux-vm / freebsd-vm
#                    jobs of integration-tests.yml)
#
# Pinned tool versions live here, not in the workflows.
# ---------------------------------------------------------------------------

REDOCLY_VERSION := 2.77.0
TSC_VERSION     := 7.0.2
# cloudflare/forge commit; bump deliberately and review the generated SDK (#181).
FORGE_REV       := 0d1877431691b07cbe3b042a4b64cb0bd8e81c03
FORGE_DIR       ?= .cache/forge
SDK_DIR         := .cache/forge-sdk
CI_JOBS         := ci-go ci-frontend ci-openapi-lint ci-forge ci-release ci-docs

ci:
	@MAKE="$(MAKE)" dev/ci/run.sh $(CI_JOBS)

ci-full: ci
	@MAKE="$(MAKE)" dev/ci/run.sh ci-integration ci-freebsd

ci-go:
	go build ./...
	go vet ./...
	go vet -tags integration ./tests/integration/
	GOOS=freebsd go vet ./...
	go test -race ./...

# node --check on a .js file misses ESM errors, so each module is checked as
# an .mjs copy (#234).
ci-frontend:
	@set -e; dir=$$(mktemp -d); \
	for f in static/app.js static/js/*.js; do \
	  out="$$dir/$$(echo "$$f" | tr / _).mjs"; \
	  cp "$$f" "$$out"; \
	  node --check "$$out" || { echo "syntax error in $$f"; exit 1; }; \
	done; \
	echo "checked $$(ls "$$dir" | wc -l | tr -d ' ') modules"; rm -r "$$dir"

ci-openapi-lint:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint api/openapi.json

# Builds Forge from FORGE_REV (not on npm yet), generates the TypeScript SDK
# from api/openapi.json and type-checks it. Forge's code runs only from that
# pinned commit. TypeScript 7's esnext lib declares Temporal.Instant, which
# Forge uses for date-time fields.
ci-forge:
	@if [ "$$(git -C $(FORGE_DIR) rev-parse HEAD 2>/dev/null)" != "$(FORGE_REV)" ]; then \
	  echo "==> Fetching forge $(FORGE_REV)"; \
	  mkdir -p $(FORGE_DIR) && git -C $(FORGE_DIR) init -q && \
	  git -C $(FORGE_DIR) fetch -q --depth 1 https://github.com/cloudflare/forge $(FORGE_REV) && \
	  git -C $(FORGE_DIR) checkout -q -f FETCH_HEAD; \
	fi
	cd $(FORGE_DIR) && cargo build --release --locked --bin forge
	rm -rf $(SDK_DIR)
	$(FORGE_DIR)/target/release/forge api/openapi.json --target ts --out $(SDK_DIR)
	@groups=$$(find $(SDK_DIR) -mindepth 1 -maxdepth 1 -type d ! -name _ | wc -l | tr -d ' '); \
	echo "generated groups: $$groups"; test "$$groups" -ge 15
	npx --yes -p typescript@$(TSC_VERSION) tsc --noEmit --strict --skipLibCheck \
	  --target esnext --module nodenext --moduleResolution nodenext --lib esnext,dom \
	  $(SDK_DIR)/index.ts

forge-rev:
	@echo $(FORGE_REV)

# The exact release recipe for all targets (#118). The version ldflag is
# checked by running the archive for this host when there is one (CI:
# linux/amd64); elsewhere (macOS) by finding the string in a linux binary.
# Output goes to .cache/dist, outside the tree `go build ./...` walks: in
# dist/, the release's rm -rf raced ci-go when `make ci` ran both at once.
SMOKE_DIST := .cache/dist

ci-release:
	$(MAKE) --no-print-directory release VERSION=v0.0.0-smoke DIST=$(SMOKE_DIST)
	@set -e; d=$(SMOKE_DIST); n=dumpstore-v0.0.0-smoke-$$(go env GOOS)-$$(go env GOARCH); \
	if [ -f $$d/$$n.tar.gz ]; then \
	  tar -xzf $$d/$$n.tar.gz -C $$d; out=$$($$d/$$n/dumpstore --version); \
	  echo "version reported: $$out"; test "$$out" = v0.0.0-smoke; \
	else \
	  n=dumpstore-v0.0.0-smoke-linux-amd64; tar -xzf $$d/$$n.tar.gz -C $$d; \
	  grep -aq v0.0.0-smoke $$d/$$n/dumpstore; \
	  echo "no archive for this host: found v0.0.0-smoke in $$n"; \
	fi

# README.md and docs/index.html must change with code (check-docs.yml).
# Locally this compares the merge base with origin/main to the working tree.
ci-docs:
	@dev/ci/check-docs.sh

# Linux VM suite as in integration-tests.yml: provisioning verified, every
# tool required (DUMPSTORE_REQUIRE_TOOLS=1).
ci-integration: vm-linux-start vm-linux-verify vm-linux-deploy
	DUMPSTORE_REQUIRE_TOOLS=1 $(MAKE) --no-print-directory test-integration

# FreeBSD VM suite, as strict as the Linux one: provisioning verified, every
# tool required.
ci-freebsd: vm-freebsd-start vm-freebsd-verify vm-freebsd-deploy
	DUMPSTORE_URL=http://localhost:8081 DUMPSTORE_VM=$(VM_FREEBSD) \
	DUMPSTORE_TEST_DISKS=/dev/vtbd2,/dev/vtbd3,/dev/vtbd4 \
	DUMPSTORE_REQUIRE_TOOLS=1 $(MAKE) --no-print-directory test-integration

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
	    Linux) \
	      if command -v apt-get >/dev/null 2>&1; then \
	        apt-get update -qq && apt-get install -y -qq ansible; \
	      else \
	        echo "error: 'ansible-playbook' not found — automatic install supports apt (Debian/Ubuntu) and FreeBSD; install Ansible with your distribution's package manager" >&2; exit 1; \
	      fi ;; \
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

# Lima reports READY even when provisioning aborted half-way; tests whose tool
# is missing would then skip. Fail here instead, with the reason (#234).
VM_REQUIRED_TOOLS := zfs zpool ansible-playbook make smbd net setfacl targetcli zfs-auto-snapshot

vm-linux-verify:
	@limactl shell --tty=false $(VM_LINUX) -- sudo grep -q '==> Provisioning complete.' /var/log/cloud-init-output.log \
	  || { echo "provision script did not complete:"; limactl shell --tty=false $(VM_LINUX) -- sudo tail -n 40 /var/log/cloud-init-output.log; exit 1; }
	@missing=""; for tool in $(VM_REQUIRED_TOOLS); do \
	  limactl shell --tty=false $(VM_LINUX) -- sh -c "command -v $$tool" >/dev/null || missing="$$missing $$tool"; \
	done; [ -z "$$missing" ] || { echo "missing in VM:$$missing"; exit 1; }
	limactl shell --tty=false $(VM_LINUX) -- sudo zpool list -H -o name tank

vm-linux-deploy:
	@echo "==> Packing source..."
	COPYFILE_DISABLE=1 tar --no-xattrs -czf /tmp/$(VM_LINUX)-src.tar.gz \
	  --exclude='.git' --exclude='dumpstore' --exclude='*.tar.gz' --exclude='.cache' --exclude='dist' \
	  -C $(CURDIR) .
	@echo "==> Copying source to $(VM_LINUX)..."
	limactl copy /tmp/$(VM_LINUX)-src.tar.gz $(VM_LINUX):/tmp/dumpstore-src.tar.gz
	@rm -f /tmp/$(VM_LINUX)-src.tar.gz
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

# Regenerate screenshots/ from the deployed Linux VM (make vm-linux-deploy first).
# Seeds demo data, captures with headless Chrome (puppeteer-core), and always
# removes the demo data again. CHROME_PATH overrides the browser location.
screenshots:
	cd dev/screenshots && npm install --no-audit --no-fund --silent
	limactl shell dumpstore-linux -- sudo sh -s up < dev/screenshots/seed.sh
	node dev/screenshots/capture.mjs; status=$$?; \
	  limactl shell dumpstore-linux -- sudo sh -s down < dev/screenshots/seed.sh; \
	  exit $$status

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

# FreeBSD counterpart of vm-linux-verify. The provision script touches
# /var/db/dumpstore-provisioned as its last step; tools in /usr/local need
# root's PATH.
VM_FREEBSD_REQUIRED_TOOLS := zfs zpool ansible-playbook make python3 smbd net ctladm smartctl

vm-freebsd-verify:
	@limactl shell --tty=false $(VM_FREEBSD) -- test -e /var/db/dumpstore-provisioned >/dev/null 2>&1 \
	  || { echo "provision script did not complete (no /var/db/dumpstore-provisioned) — rebuild: make vm-freebsd-destroy vm-freebsd-start"; exit 1; }
	@missing=""; for tool in $(VM_FREEBSD_REQUIRED_TOOLS); do \
	  limactl shell --tty=false $(VM_FREEBSD) -- sudo sh -c "command -v $$tool" >/dev/null 2>&1 || missing="$$missing $$tool"; \
	done; [ -z "$$missing" ] || { echo "missing in VM:$$missing"; exit 1; }
	@limactl shell --tty=false $(VM_FREEBSD) -- sudo zpool list -H -o name tank 2>/dev/null | grep -x tank

vm-freebsd-deploy:
	@echo "==> Packing source..."
	COPYFILE_DISABLE=1 tar --no-xattrs -czf /tmp/$(VM_FREEBSD)-src.tar.gz \
	  --exclude='.git' --exclude='dumpstore' --exclude='*.tar.gz' --exclude='.cache' --exclude='dist' \
	  -C $(CURDIR) .
	@echo "==> Copying source to $(VM_FREEBSD)..."
	limactl copy /tmp/$(VM_FREEBSD)-src.tar.gz $(VM_FREEBSD):/tmp/dumpstore-src.tar.gz
	@rm -f /tmp/$(VM_FREEBSD)-src.tar.gz
	@echo "==> Running make install in VM..."
	limactl shell --tty=false $(VM_FREEBSD) -- sudo sh -c \
	  'rm -rf /tmp/dumpstore-src && mkdir /tmp/dumpstore-src && \
	   tar --no-xattrs -xzf /tmp/dumpstore-src.tar.gz -C /tmp/dumpstore-src && \
	   cd /tmp/dumpstore-src && \
	   PATH=/usr/local/go/bin:/usr/local/bin:$$PATH make install \
	     >/tmp/dumpstore-install.log 2>&1 </dev/null; \
	   rc=$$?; cat /tmp/dumpstore-install.log; exit $$rc'
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
