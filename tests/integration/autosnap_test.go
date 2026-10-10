//go:build integration

package integration

import (
	"net/http"
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
// Takeover/release of the OS daemon is not exercised: on Debian's cron-based
// zfs-auto-snapshot it cannot restore the host's prior state (#152).
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
