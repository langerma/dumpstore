//go:build integration

package integration

import (
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestISCSITarget exports a zvol as an iSCSI target and removes it again,
// on targetcli/LIO (Linux) or ctld (FreeBSD) — see iscsiFixture. On ctld it
// also covers the init gate, importing an old block-patched ctl.conf, several
// targets sharing one portal, and survival of a ctld restart (#186).
func TestISCSITarget(t *testing.T) {
	zvol := testPool + "/itest-vol"
	const iqn = "iqn.2026-10.local.dumpstore:itest"

	destroyDatasetInVM(zvol)
	ctld := iscsiFixture(t, zvol, iqn)
	t.Cleanup(func() { destroyDatasetInVM(zvol) })
	createZvol(t, zvol)

	// CHAP without a password is refused before anything runs.
	apiStatus(t, http.StatusBadRequest, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol, "iqn": iqn, "auth_mode": "chap", "chap_user": "itest",
	})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol, "iqn": "not-an-iqn", "auth_mode": "none",
	})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol, "iqn": iqn, "auth_mode": "chap", "chap_user": "itest", "chap_password": `bad"secret`,
	})

	if ctld {
		testCtld(t, zvol, iqn)
		return
	}

	assertTasks(t, apiOK(t, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol, "iqn": iqn, "auth_mode": "none",
	}))
	if !hasTarget(t, iqn, zvol) {
		t.Fatalf("target %s not listed after create", iqn)
	}

	assertTasks(t, apiOK(t, "DELETE", v1("/iscsi/targets/%s", iqn), nil))
	if hasTarget(t, iqn, zvol) {
		t.Fatalf("target %s still listed after delete", iqn)
	}
	// The zvol is looked up server-side; an unknown IQN is a 404.
	apiStatus(t, http.StatusNotFound, "DELETE", v1("/iscsi/targets/%s", iqn), nil)
	// Deleting the target leaves the zvol itself alone.
	if _, ok := datasetByName(t, zvol); !ok {
		t.Fatalf("zvol %s gone after target delete", zvol)
	}
}

// legacyCtlConf is the format the pre-#186 block-patching playbook wrote:
// one portal group per target, wrapped in DUMPSTORE markers, no auth clause.
const legacyCtlConf = `# BEGIN DUMPSTORE PORTAL pg-iqn-2026-10-local-dumpst
portal-group pg-iqn-2026-10-local-dumpst {
    listen 0.0.0.0:3260
}
# END DUMPSTORE PORTAL pg-iqn-2026-10-local-dumpst
# BEGIN DUMPSTORE TARGET iqn.2026-10.local.dumpstore:itest
target iqn.2026-10.local.dumpstore:itest {
    portal-group pg-iqn-2026-10-local-dumpst


    lun 0 {
        path /dev/zvol/ZVOL
    }
}
# END DUMPSTORE TARGET iqn.2026-10.local.dumpstore:itest
`

func testCtld(t *testing.T, zvol, iqn string) {
	zvol2, zvol3 := testPool+"/itest-vol2", testPool+"/itest-vol3"
	const iqn2, iqn3 = "iqn.2026-11.local.dumpstore:itest2", "iqn.2026-11.local.dumpstore:itest3"
	for _, z := range []string{zvol2, zvol3} {
		destroyDatasetInVM(z)
		t.Cleanup(func() { destroyDatasetInVM(z) })
		createZvol(t, z)
	}

	// Gate: writes are refused until dumpstore owns ctl.conf.
	body := apiStatus(t, http.StatusConflict, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol2, "iqn": iqn2, "auth_mode": "none",
	})
	if !strings.Contains(string(body), "/api/v1/iscsi/init") {
		t.Fatalf("gate error does not point at init: %s", truncate(body, 300))
	}
	apiStatus(t, http.StatusConflict, "DELETE", v1("/iscsi/targets/%s", iqn), nil)

	// Seed an old block-patched file; init imports its target.
	vmExec(t, "cat > /etc/ctl.conf <<'EOF'\n"+strings.ReplaceAll(legacyCtlConf, "ZVOL", zvol)+"EOF\nchmod 600 /etc/ctl.conf")
	st := decode[struct {
		Backend     string `json:"backend"`
		Initialized bool   `json:"initialized"`
		ConfExists  bool   `json:"conf_exists"`
	}](t, apiOK(t, "GET", "/api/v1/iscsi/status", nil))
	if st.Backend != "freebsd" || st.Initialized || !st.ConfExists {
		t.Fatalf("status before init = %+v", st)
	}
	assertTasks(t, apiOK(t, "POST", "/api/v1/iscsi/init", nil))
	vmExec(t, "head -1 /etc/ctl.conf | grep -q '^# Managed by dumpstore' && ! grep -q DUMPSTORE /etc/ctl.conf && "+
		"test -f /etc/ctl.conf.pre-dumpstore && grep -q 'DUMPSTORE TARGET' /etc/ctl.conf.pre-dumpstore")
	if !hasTarget(t, iqn, zvol) {
		t.Fatalf("legacy target %s not imported", iqn)
	}
	// Init is idempotent and keeps the original backup.
	assertTasks(t, apiOK(t, "POST", "/api/v1/iscsi/init", nil))
	vmExec(t, "grep -q 'DUMPSTORE TARGET' /etc/ctl.conf.pre-dumpstore")

	// Second target (auth none) and third (CHAP) on the same default portal:
	// before #186 the second one broke ctld with "Address already in use".
	assertTasks(t, apiOK(t, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol2, "iqn": iqn2, "auth_mode": "none",
	}))
	assertTasks(t, apiOK(t, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol3, "iqn": iqn3, "auth_mode": "chap", "chap_user": "itest", "chap_password": "itest-secret-123",
	}))
	if n := strings.TrimSpace(vmExec(t, "grep -c '^portal-group ' /etc/ctl.conf")); n != "1" {
		t.Fatalf("want one shared portal group, got %s", n)
	}
	if b := apiOK(t, "GET", "/api/v1/iscsi/targets", nil); strings.Contains(string(b), "itest-secret-123") {
		t.Fatalf("CHAP secret exposed by GET /api/v1/iscsi/targets")
	}
	wantPorts(t, iqn, iqn2, iqn3)

	vmExec(t, "service ctld restart >/dev/null 2>&1 </dev/null")
	wantPorts(t, iqn, iqn2, iqn3)

	// Conflicts: duplicate IQN, zvol already exported, unknown target.
	apiStatus(t, http.StatusConflict, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol, "iqn": iqn2, "auth_mode": "none",
	})
	apiStatus(t, http.StatusConflict, "POST", "/api/v1/iscsi/targets", map[string]any{
		"zvol": zvol2, "iqn": "iqn.2026-11.local.dumpstore:other", "auth_mode": "none",
	})
	apiStatus(t, http.StatusNotFound, "DELETE", v1("/iscsi/targets/%s", "iqn.2026-11.local.dumpstore:nope"), nil)

	del := func(iqn string) {
		t.Helper()
		assertTasks(t, apiOK(t, "DELETE", v1("/iscsi/targets/%s", iqn), nil))
	}
	del(iqn2)
	wantPorts(t, iqn, iqn3)
	if hasTarget(t, iqn2, zvol2) {
		t.Fatalf("target %s still listed after delete", iqn2)
	}
	del(iqn)
	del(iqn3)
	wantPorts(t)
	if _, ok := datasetByName(t, zvol); !ok {
		t.Fatalf("zvol %s gone after target delete", zvol)
	}
}

// wantPorts asserts ctld's active iSCSI ports for this test's targets (IQNs
// containing ":itest") are exactly iqns. Other kernel ports — e.g. orphans
// left by a ctld that crashed — are not dumpstore's and are ignored.
func wantPorts(t *testing.T, iqns ...string) {
	t.Helper()
	var got []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		got = got[:0]
		for _, f := range strings.Fields(vmExec(t, "ctladm portlist -f iscsi -q")) {
			if name, _, found := strings.Cut(f, ",t,"); found && strings.Contains(name, ":itest") {
				got = append(got, name)
			}
		}
		sort.Strings(got)
		want := append([]string(nil), iqns...)
		sort.Strings(want)
		if strings.Join(got, " ") == strings.Join(want, " ") {
			return
		}
	}
	t.Fatalf("ctladm portlist: got %v, want %v", got, iqns)
}

func createZvol(t *testing.T, zvol string) {
	t.Helper()
	apiOK(t, "POST", "/api/v1/datasets", map[string]any{"name": zvol, "type": "volume", "volsize": "64M"})
	// udev creates /dev/zvol/<name> asynchronously after zfs create.
	waitFor(t, "zvol device node", 30*time.Second, func() bool {
		_, err := vmExecErr("test -e /dev/zvol/" + zvol)
		return err == nil
	})
}

func hasTarget(t *testing.T, iqn, zvol string) bool {
	t.Helper()
	// null when targets exist but none are zvol-backed.
	for _, tg := range decode[[]struct {
		IQN      string `json:"iqn"`
		ZvolName string `json:"zvol_name"`
	}](t, apiOK(t, "GET", "/api/v1/iscsi/targets", nil)) {
		if tg.IQN == iqn && tg.ZvolName == zvol {
			return true
		}
	}
	return false
}
