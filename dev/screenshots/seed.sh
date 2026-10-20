#!/bin/sh
# seed.sh — populate the Linux dev VM with demo data for screenshots, or
# remove it again. Runs inside the VM as root against the deployed instance:
#
#   limactl shell dumpstore-linux -- sudo sh -s up   < dev/screenshots/seed.sh
#   limactl shell dumpstore-linux -- sudo sh -s down < dev/screenshots/seed.sh
#
# Plain ZFS state and users are created directly; everything dumpstore owns
# (Samba config, usershares, iSCSI, replication, scrub schedule) goes through
# the API so the config stays dumpstore-rendered. "down" undoes exactly what
# "up" created and leaves the VM as the integration tests expect it.
set -eu

URL=${DUMPSTORE_URL:-http://localhost:8080}
POOL=tank
DEMO_DATASETS="media documents homes backups vms"
DEMO_USERS="alice bob carol"
DEMO_GROUP=media
WSDD_MARKER=/var/tmp/dumpstore-demo-wsdd
CK=$(mktemp)
trap 'rm -f "$CK"' EXIT

api() { # api METHOD PATH [JSON] — prints the server's error and fails on non-2xx
	if [ $# -ge 3 ]; then
		out=$(curl -sS --fail-with-body -b "$CK" -X "$1" -H 'Content-Type: application/json' -d "$3" "$URL$2") ||
			{ echo "$1 $2 failed: $out" >&2; return 1; }
	else
		out=$(curl -sS --fail-with-body -b "$CK" -X "$1" "$URL$2") ||
			{ echo "$1 $2 failed: $out" >&2; return 1; }
	fi
}
login() {
	curl -fsS -c "$CK" -o /dev/null -d "username=${DUMPSTORE_USER:-admin}&password=${DUMPSTORE_PASS:-admin}" "$URL/auth/login"
}
fill() { # fill FILE MiB random|text
	if [ "$3" = random ]; then
		head -c "$(($2 * 1048576))" /dev/urandom >"$1"
	else
		head -c "$(($2 * 786432))" /dev/urandom | base64 >"$1" # text-like, ~1.3x compressible
	fi
}

up() {
	login
	zfs list "$POOL/media" >/dev/null 2>&1 && { echo "already seeded — run 'down' first" >&2; exit 1; }

	# Finished jobs left behind by the integration suite would crowd the Jobs tab.
	curl -fsS -b "$CK" "$URL/api/jobs" | python3 -c 'import json,sys
for j in json.load(sys.stdin):
    if "itest" in " ".join(j["args"]) and j["status"] not in ("running", "queued"): print(j["id"])' |
		while read -r jid; do api DELETE "/api/jobs/$jid"; done

	echo "==> users and groups"
	api POST /api/groups "{\"groupname\":\"$DEMO_GROUP\"}"
	for u in $DEMO_USERS; do
		api POST /api/users "{\"username\":\"$u\",\"shell\":\"/bin/bash\",\"create_group\":true,\"groups\":\"$DEMO_GROUP\",\"password\":\"demo-$u-pw\",\"smb_user\":true}"
	done

	echo "==> datasets and data"
	zfs create -o compression=zstd -o recordsize=1M "$POOL/media"
	zfs create -o compression=zstd -o acltype=posix -o xattr=sa "$POOL/documents"
	zfs create "$POOL/homes"
	for u in alice bob; do
		zfs create -o quota=2G "$POOL/homes/$u"
		chown "$u:$u" "/$POOL/homes/$u"
	done
	zfs create "$POOL/backups"
	zfs create -o quota=50G "$POOL/backups/timemachine"
	zfs create "$POOL/vms"
	zfs create -s -V 4G -o volblocksize=16K "$POOL/vms/win11"
	for d in $DEMO_DATASETS; do zfs set dumpstore:demo=1 "$POOL/$d"; done

	mkdir -p "/$POOL/media/movies" "/$POOL/media/music"
	fill "/$POOL/media/movies/holiday-2025.mkv" 700 random
	fill "/$POOL/media/movies/birthday.mp4" 350 random
	fill "/$POOL/media/music/album.flac" 120 random
	fill "/$POOL/homes/alice/thesis.pdf" 180 random
	fill "/$POOL/homes/bob/photos.tar" 90 random
	chown -R alice:alice "/$POOL/homes/alice"; chown -R bob:bob "/$POOL/homes/bob"

	mkdir -p "/$POOL/documents/projects" "/$POOL/documents/invoices"
	fill "/$POOL/documents/projects/plan.txt" 60 text
	fill "/$POOL/documents/invoices/2026-q3.csv" 40 text
	zfs snapshot "$POOL/documents@before-cleanup"
	fill "/$POOL/documents/projects/plan.txt" 70 text
	fill "/$POOL/documents/projects/notes.md" 20 text
	rm "/$POOL/documents/invoices/2026-q3.csv"
	zfs snapshot "$POOL/documents@after-cleanup"
	fill "/$POOL/documents/projects/roadmap.md" 5 text
	for s in daily-2026-10-15-0300 daily-2026-10-16-0300 daily-2026-10-17-0300 weekly-2026-10-12-0400; do
		zfs snapshot "$POOL/media@zfs-auto-snap_$s"
	done
	zfs snapshot -r "$POOL/homes@pre-upgrade"
	mkdir -p "/$POOL/documents/reports"
	fill "/$POOL/documents/reports/q3-summary.txt" 25 text
	chown -R alice "/$POOL/documents/projects"
	chown -R bob "/$POOL/documents/reports"
	zfs set userquota@alice=1G userquota@bob=500M "$POOL/documents"

	echo "==> permissions and NFS"
	chgrp -R "$DEMO_GROUP" "/$POOL/documents"
	api POST "/api/acl/$POOL/documents" '{"ace":"user:alice:rwx","recursive":true}'
	api POST "/api/acl/$POOL/documents" "{\"ace\":\"group:$DEMO_GROUP:r-x\",\"recursive\":true}"
	api PATCH "/api/datasets/$POOL/documents" '{"sharenfs":"rw=@192.168.1.0/24,no_root_squash"}'
	api PUT "/api/auto-snapshot/$POOL/media" '{"com.sun:auto-snapshot":"true","com.sun:auto-snapshot:daily":"7","com.sun:auto-snapshot:weekly":"4"}'

	echo "==> Samba"
	api POST /api/smb/init
	api POST "/api/smb-share/$POOL/media" '{"sharename":"media"}'
	api POST /api/smb/homes "{\"path\":\"/$POOL/homes/%S\",\"browseable\":\"no\",\"read_only\":\"no\",\"create_mask\":\"0644\",\"directory_mask\":\"0755\"}"
	api POST /api/smb/timemachine "{\"sharename\":\"timemachine\",\"path\":\"/$POOL/backups/timemachine\",\"max_size\":\"500G\",\"valid_users\":\"alice bob\"}"
	if command -v apt-get >/dev/null && ! dpkg -s wsdd-server >/dev/null 2>&1; then
		DEBIAN_FRONTEND=noninteractive apt-get install -y -q wsdd-server >/dev/null
		touch "$WSDD_MARKER"
	fi
	api POST /api/smb/wsdd

	echo "==> iSCSI, replication, scrub"
	api POST /api/iscsi-targets "{\"zvol\":\"$POOL/vms/win11\",\"iqn\":\"iqn.2026-10.lan.nas:win11\",\"portal_ip\":\"0.0.0.0\",\"portal_port\":\"3260\",\"auth_mode\":\"none\",\"initiators\":[]}"
	api POST /api/replication "{\"name\":\"documents-nightly\",\"source\":\"$POOL/documents\",\"target\":\"$POOL/backups/documents\",\"schedule\":\"0 3 * * *\",\"retention_count\":7}"
	id=$(curl -fsS -b "$CK" "$URL/api/replication" | python3 -c 'import json,sys; print(next(t["id"] for t in json.load(sys.stdin) if t["name"]=="documents-nightly"))')
	api POST "/api/replication/$id/run"
	api PUT "/api/scrub-schedule/$POOL"
	zpool scrub -w "$POOL"
	echo "==> seeded"
}

down() {
	login
	id=$(curl -fsS -b "$CK" "$URL/api/replication" | python3 -c 'import json,sys; print(next((t["id"] for t in json.load(sys.stdin) if t["name"]=="documents-nightly"), ""))')
	[ -n "$id" ] && api DELETE "/api/replication/$id"
	api DELETE "/api/iscsi-targets?iqn=iqn.2026-10.lan.nas:win11&zvol=$POOL/vms/win11" || true
	api DELETE /api/smb/timemachine/timemachine || true
	api DELETE /api/smb/homes || true
	api DELETE "/api/smb-share/$POOL/media?name=media" || true
	api DELETE "/api/scrub-schedule/$POOL" || true
	if [ -e "$WSDD_MARKER" ]; then # only remove wsdd if "up" installed it
		DEBIAN_FRONTEND=noninteractive apt-get purge -y -q wsdd-server wsdd >/dev/null 2>&1 || true
		rm -f /etc/default/wsdd /etc/default/wsdd.*~ "$WSDD_MARKER"
	fi
	for d in $DEMO_DATASETS; do
		zfs list "$POOL/$d" >/dev/null 2>&1 && zfs destroy -r "$POOL/$d"
	done
	for u in $DEMO_USERS; do
		pdbedit -x "$u" >/dev/null 2>&1 || true
		id "$u" >/dev/null 2>&1 && userdel -r "$u" 2>/dev/null || true
		getent group "$u" >/dev/null && groupdel "$u" || true
	done
	getent group "$DEMO_GROUP" >/dev/null && groupdel "$DEMO_GROUP" || true
	echo "==> demo data removed"
}

case "${1:-}" in
up) up ;;
down) down ;;
*) echo "usage: seed.sh up|down" >&2; exit 2 ;;
esac
