#!/bin/sh
# vm-runner-setup.sh — prepare a GitHub ubuntu runner for the Lima VM jobs in
# integration-tests.yml: KVM for the runner user, QEMU, and a
# checksum-verified Lima. CI-only (uses sudo and apt).
#
# Lima is pinned by version and SHA-256 here; FreeBSD guests need >= 2.1.
# Bump both together (sha from the release's SHA256SUMS).
set -eu

LIMA_VERSION=2.3.0
LIMA_SHA256=cc8ba97f1c801b899f7c9eda9919dfc4ce4fa2c140c53e5add15aa2d0a1936f9

echo "==> Enabling KVM for the runner user"
echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' \
  | sudo tee /etc/udev/rules.d/99-kvm4all.rules >/dev/null
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=kvm

echo "==> Installing QEMU"
# Tolerate index failures from unrelated third-party repos on the runner
# image; everything below comes from the default Ubuntu archive.
sudo apt-get update -qq || true
# ovmf: UEFI firmware for x86_64 guests. xorriso: Lima builds the FreeBSD
# guest's cidata ISO with it on non-macOS hosts.
sudo apt-get install -y -qq -o Acquire::Retries=5 --no-install-recommends \
  qemu-system-x86 qemu-utils ovmf xorriso

echo "==> Installing Lima $LIMA_VERSION"
tarball="lima-$LIMA_VERSION-Linux-x86_64.tar.gz"
curl -fsSL --retry 3 -o "/tmp/$tarball" \
  "https://github.com/lima-vm/lima/releases/download/v$LIMA_VERSION/$tarball"
echo "$LIMA_SHA256  /tmp/$tarball" | sha256sum -c -
sudo tar -C /usr/local -xzf "/tmp/$tarball"
rm -f "/tmp/$tarball"
limactl --version
