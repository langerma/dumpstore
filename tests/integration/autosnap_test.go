//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

type zfsProp struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

const (
	propAutoSnap      = "com.sun:auto-snapshot"
	propAutoSnapDaily = "com.sun:auto-snapshot:daily"
)

// TestAutoSnapshotProperties sets the per-dataset auto-snapshot properties,
// reads them back through both read endpoints, and clears them to inherit.
func TestAutoSnapshotProperties(t *testing.T) {
	ds := testPool + "/itest-autosnap"
	createDataset(t, ds)

	assertTasks(t, apiOK(t, "PUT", "/api/auto-snapshot/"+ds, map[string]string{
		propAutoSnap: "true", propAutoSnapDaily: "7",
	}))
	props := decode[map[string]zfsProp](t, apiOK(t, "GET", "/api/auto-snapshot/"+ds, nil))
	if props[propAutoSnap].Value != "true" || props[propAutoSnapDaily].Value != "7" ||
		props[propAutoSnap].Source != "local" {
		t.Fatalf("props after set: %+v", props)
	}
	all := decode[map[string]map[string]zfsProp](t, apiOK(t, "GET", "/api/auto-snapshot-schedules", nil))
	if all[ds][propAutoSnapDaily].Value != "7" {
		t.Fatalf("schedules list for %s: %+v", ds, all[ds])
	}

	// Empty string means inherit.
	assertTasks(t, apiOK(t, "PUT", "/api/auto-snapshot/"+ds, map[string]string{
		propAutoSnap: "", propAutoSnapDaily: "",
	}))
	props = decode[map[string]zfsProp](t, apiOK(t, "GET", "/api/auto-snapshot/"+ds, nil))
	if props[propAutoSnap].Source == "local" || props[propAutoSnapDaily].Source == "local" {
		t.Fatalf("props still local after inherit: %+v", props)
	}

	apiStatus(t, http.StatusBadRequest, "PUT", "/api/auto-snapshot/"+ds, map[string]string{propAutoSnap: "yes"})
	apiStatus(t, http.StatusBadRequest, "PUT", "/api/auto-snapshot/"+ds, map[string]string{propAutoSnapDaily: "0"})
	apiStatus(t, http.StatusNotFound, "PUT", "/api/auto-snapshot/"+testPool+"/itest-nonexistent", map[string]string{propAutoSnap: "true"})
}

// TestScrubSchedule adds and removes the test pool from the scrub schedule.
// The handler rewrites a host config file (/etc/default/zfs on Linux), so it
// is backed up first and restored byte-for-byte afterwards.
func TestScrubSchedule(t *testing.T) {
	const conf, backup = "/etc/default/zfs", "/tmp/itest-default-zfs"
	if _, err := vmExecErr("test -d /etc/default"); err != nil {
		t.Skip("no /etc/default — scrub schedules are Linux zfsutils only in this test")
	}
	vmExec(t, "if [ -e "+conf+" ]; then cp -p "+conf+" "+backup+"; else rm -f "+backup+"; fi")
	t.Cleanup(func() {
		_, _ = vmExecErr("if [ -e " + backup + " ]; then mv " + backup + " " + conf + "; else rm -f " + conf + "; fi")
	})

	scheduled := func() bool {
		r := decode[struct {
			Schedules []struct {
				Pool string `json:"pool"`
			} `json:"schedules"`
		}](t, apiOK(t, "GET", "/api/scrub-schedules", nil))
		for _, s := range r.Schedules {
			if s.Pool == testPool {
				return true
			}
		}
		return false
	}
	assertTasks(t, apiOK(t, "PUT", "/api/scrub-schedule/"+testPool, map[string]any{}))
	if !scheduled() {
		t.Fatalf("pool %s not in scrub schedules after PUT", testPool)
	}
	assertTasks(t, apiOK(t, "DELETE", "/api/scrub-schedule/"+testPool, nil))
	if scheduled() {
		t.Fatalf("pool %s still in scrub schedules after DELETE", testPool)
	}
	apiStatus(t, http.StatusBadRequest, "PUT", "/api/scrub-schedule/bad@pool", map[string]any{})
}

type autosnapStatus struct {
	OSDaemonActive   bool `json:"os_daemon_active"`
	DumpstoreManaged bool `json:"dumpstore_managed"`
}

// autosnapCronFiles mirrors autosnap.CronFiles (Debian/Ubuntu packaging).
const autosnapCronFiles = "/etc/cron.d/zfs-auto-snapshot /etc/cron.hourly/zfs-auto-snapshot" +
	" /etc/cron.daily/zfs-auto-snapshot /etc/cron.weekly/zfs-auto-snapshot /etc/cron.monthly/zfs-auto-snapshot"

// TestAutosnapTakeoverRelease hands auto-snapshot execution from the OS
// daemon to dumpstore and back, and checks the host's cron entry points come
// back byte-for-byte (#152).
func TestAutosnapTakeoverRelease(t *testing.T) {
	skipUnlessVMTool(t, "zfs-auto-snapshot")
	status := func() autosnapStatus {
		return decode[autosnapStatus](t, apiOK(t, "GET", "/api/auto-snapshot/status", nil))
	}
	fingerprint := func() string {
		return vmExec(t, "for f in "+autosnapCronFiles+"; do [ -e $f ] && md5sum $f; done; true")
	}
	before, initial := fingerprint(), status()
	if !initial.OSDaemonActive || initial.DumpstoreManaged {
		t.Skipf("expected the OS daemon to own auto-snapshots initially, got %+v", initial)
	}
	t.Cleanup(func() {
		if st, _ := api(t, "POST", "/api/auto-snapshot/release", nil); st != http.StatusOK {
			t.Errorf("cleanup release returned %d", st)
		}
		// Backstop: put any still-disabled entry point back.
		_, _ = vmExecErr("for f in " + autosnapCronFiles + "; do [ -e $f.dumpstore-disabled ] && [ ! -e $f ] && mv $f.dumpstore-disabled $f; done; true")
	})

	// Twice: takeover must be idempotent.
	for range 2 {
		assertTasks(t, apiOK(t, "POST", "/api/auto-snapshot/takeover", nil))
	}
	if st := status(); st.OSDaemonActive || !st.DumpstoreManaged {
		t.Fatalf("status after takeover: %+v, want daemon inactive and dumpstore managed", st)
	}
	if left := fingerprint(); strings.TrimSpace(left) != "" {
		t.Fatalf("OS cron entry points still active after takeover:\n%s", left)
	}

	assertTasks(t, apiOK(t, "POST", "/api/auto-snapshot/release", nil))
	if st := status(); !st.OSDaemonActive || st.DumpstoreManaged {
		t.Fatalf("status after release: %+v, want daemon active and dumpstore not managed", st)
	}
	if after := fingerprint(); after != before {
		t.Fatalf("cron entry points not restored:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
