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

	assertTasks(t, apiOK(t, "PUT", v1("/datasets/%s/auto-snapshot", ds), map[string]string{
		propAutoSnap: "true", propAutoSnapDaily: "7",
	}))
	props := decode[map[string]zfsProp](t, apiOK(t, "GET", v1("/datasets/%s/auto-snapshot", ds), nil))
	if props[propAutoSnap].Value != "true" || props[propAutoSnapDaily].Value != "7" ||
		props[propAutoSnap].Source != "local" {
		t.Fatalf("props after set: %+v", props)
	}
	all := decode[map[string]map[string]zfsProp](t, apiOK(t, "GET", "/api/v1/auto-snapshot/schedules", nil))
	if all[ds][propAutoSnapDaily].Value != "7" {
		t.Fatalf("schedules list for %s: %+v", ds, all[ds])
	}

	// Empty string means inherit.
	assertTasks(t, apiOK(t, "PUT", v1("/datasets/%s/auto-snapshot", ds), map[string]string{
		propAutoSnap: "", propAutoSnapDaily: "",
	}))
	props = decode[map[string]zfsProp](t, apiOK(t, "GET", v1("/datasets/%s/auto-snapshot", ds), nil))
	if props[propAutoSnap].Source == "local" || props[propAutoSnapDaily].Source == "local" {
		t.Fatalf("props still local after inherit: %+v", props)
	}

	apiStatus(t, http.StatusBadRequest, "PUT", v1("/datasets/%s/auto-snapshot", ds), map[string]string{propAutoSnap: "yes"})
	apiStatus(t, http.StatusBadRequest, "PUT", v1("/datasets/%s/auto-snapshot", ds), map[string]string{propAutoSnapDaily: "0"})
	apiStatus(t, http.StatusNotFound, "PUT", v1("/datasets/%s/auto-snapshot", testPool+"/itest-nonexistent"), map[string]string{propAutoSnap: "true"})
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
		}](t, apiOK(t, "GET", "/api/v1/pools/scrub-schedules", nil))
		for _, s := range r.Schedules {
			if s.Pool == testPool {
				return true
			}
		}
		return false
	}
	assertTasks(t, apiOK(t, "PUT", v1("/pools/%s/scrub-schedule", testPool), map[string]any{}))
	if !scheduled() {
		t.Fatalf("pool %s not in scrub schedules after PUT", testPool)
	}
	assertTasks(t, apiOK(t, "DELETE", v1("/pools/%s/scrub-schedule", testPool), nil))
	if scheduled() {
		t.Fatalf("pool %s still in scrub schedules after DELETE", testPool)
	}
	apiStatus(t, http.StatusBadRequest, "PUT", v1("/pools/%s/scrub-schedule", "bad@pool"), map[string]any{})
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
	if out, _ := vmExecErr("uname -s"); strings.TrimSpace(out) != "Linux" {
		t.Skip("cron.d takeover is Linux-only; FreeBSD is TestAutosnapTakeoverReleaseFreeBSD")
	}
	skipUnlessVMTool(t, "zfs-auto-snapshot")
	status := func() autosnapStatus {
		return decode[autosnapStatus](t, apiOK(t, "GET", "/api/v1/auto-snapshot/status", nil))
	}
	fingerprint := func() string {
		return vmExec(t, "for f in "+autosnapCronFiles+"; do [ -e $f ] && md5sum $f; done; true")
	}
	before, initial := fingerprint(), status()
	if !initial.OSDaemonActive || initial.DumpstoreManaged {
		t.Skipf("expected the OS daemon to own auto-snapshots initially, got %+v", initial)
	}
	t.Cleanup(func() {
		if st, _ := api(t, "POST", "/api/v1/auto-snapshot/release", nil); st != http.StatusOK {
			t.Errorf("cleanup release returned %d", st)
		}
		// Backstop: put any still-disabled entry point back.
		_, _ = vmExecErr("for f in " + autosnapCronFiles + "; do [ -e $f.dumpstore-disabled ] && [ ! -e $f ] && mv $f.dumpstore-disabled $f; done; true")
	})

	// Twice: takeover must be idempotent.
	for range 2 {
		assertTasks(t, apiOK(t, "POST", "/api/v1/auto-snapshot/takeover", nil))
	}
	if st := status(); st.OSDaemonActive || !st.DumpstoreManaged {
		t.Fatalf("status after takeover: %+v, want daemon inactive and dumpstore managed", st)
	}
	if left := fingerprint(); strings.TrimSpace(left) != "" {
		t.Fatalf("OS cron entry points still active after takeover:\n%s", left)
	}

	assertTasks(t, apiOK(t, "POST", "/api/v1/auto-snapshot/release", nil))
	if st := status(); !st.OSDaemonActive || st.DumpstoreManaged {
		t.Fatalf("status after release: %+v, want daemon active and dumpstore not managed", st)
	}
	if after := fingerprint(); after != before {
		t.Fatalf("cron entry points not restored:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestAutosnapTakeoverReleaseFreeBSD hands zfs-periodic's snapshot switches
// to dumpstore and back (#197): settings spread over two periodic.conf files
// are set to NO by takeover and restored exactly by release.
func TestAutosnapTakeoverReleaseFreeBSD(t *testing.T) {
	if out, _ := vmExecErr("uname -s"); strings.TrimSpace(out) != "FreeBSD" {
		t.Skip("periodic.conf takeover is FreeBSD-only")
	}
	const (
		conf   = "/etc/periodic.conf"
		local  = "/etc/periodic.conf.local"
		usr    = "/usr/local/etc/periodic.conf"
		record = "/var/db/dumpstore/autosnap-disabled-periodic.json"
		files  = conf + " " + local + " " + usr + " " + record
	)
	status := func() autosnapStatus {
		return decode[autosnapStatus](t, apiOK(t, "GET", "/api/v1/auto-snapshot/status", nil))
	}
	initial := status()
	// Set the files aside (absent ones are marked) and put them back after.
	vmExec(t, "for f in "+files+"; do rm -f $f.itest-bak $f.itest-absent; if [ -e $f ]; then cp -p $f $f.itest-bak; else touch $f.itest-absent; fi; done")
	t.Cleanup(func() {
		_, _ = api(t, "POST", "/api/v1/auto-snapshot/release", nil)
		if initial.DumpstoreManaged {
			// Re-register dumpstore's scheduler; the file restore below
			// undoes what this takeover writes.
			_, _ = api(t, "POST", "/api/v1/auto-snapshot/takeover", nil)
		}
		restore := "for f in " + files + "; do if [ -e $f.itest-bak ]; then mv $f.itest-bak $f; elif [ -e $f.itest-absent ]; then rm -f $f $f.itest-absent; fi; done"
		if out, err := vmExecErr(restore); err != nil {
			t.Errorf("restoring periodic.conf files failed — fix the VM by hand: %v\n%s", err, out)
		}
	})

	vmExec(t, "rm -f "+usr+" "+record+
		"; printf '# itest\\ndaily_zfs_snapshot_enable=\"YES\"\\nweekly_zfs_snapshot_enable=\"NO\"\\n' > "+conf+
		"; printf 'hourly_zfs_snapshot_enable=\"YES\"\\n' > "+local)
	fingerprint := func() string { return vmExec(t, "md5 -r "+conf+" "+local+"; [ ! -e "+usr+" ] && echo no-usr; true") }
	before := fingerprint()
	if st := status(); !st.OSDaemonActive {
		t.Fatalf("status with daily/hourly YES: %+v, want daemon active", st)
	}

	// Twice: takeover must be idempotent and keep the first record.
	for range 2 {
		assertTasks(t, apiOK(t, "POST", "/api/v1/auto-snapshot/takeover", nil))
	}
	if st := status(); st.OSDaemonActive || !st.DumpstoreManaged {
		t.Fatalf("status after takeover: %+v, want daemon inactive and dumpstore managed", st)
	}
	got := vmExec(t, "sysrc -f "+conf+" -n daily_zfs_snapshot_enable weekly_zfs_snapshot_enable; sysrc -f "+local+" -n hourly_zfs_snapshot_enable")
	if strings.Join(strings.Fields(got), " ") != "NO NO NO" {
		t.Fatalf("switches after takeover: %q, want NO NO NO", got)
	}

	assertTasks(t, apiOK(t, "POST", "/api/v1/auto-snapshot/release", nil))
	if st := status(); !st.OSDaemonActive || st.DumpstoreManaged {
		t.Fatalf("status after release: %+v, want daemon active and dumpstore not managed", st)
	}
	if after := fingerprint(); after != before {
		t.Fatalf("periodic.conf files not restored:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	vmExec(t, "[ ! -e "+record+" ]")
}

// TestAutosnapZfstoolsCron: a crontab running zfstools' zfs-auto-snapshot is
// detected as the active OS daemon, and takeover is refused with 409 because
// dumpstore never edits a crontab (#208).
func TestAutosnapZfstoolsCron(t *testing.T) {
	if out, _ := vmExecErr("uname -s"); strings.TrimSpace(out) != "FreeBSD" {
		t.Skip("zfstools cron detection is FreeBSD-only")
	}
	const tab = "/etc/cron.d/itest-zfstools"
	type status struct {
		OSDaemon       string   `json:"os_daemon"`
		OSDaemonActive bool     `json:"os_daemon_active"`
		CronFiles      []string `json:"cron_files"`
	}
	get := func() status { return decode[status](t, apiOK(t, "GET", "/api/v1/auto-snapshot/status", nil)) }
	t.Cleanup(func() { _, _ = vmExecErr("rm -f " + tab) })

	vmExec(t, "printf '#0 * * * * root /usr/local/sbin/zfs-auto-snapshot hourly 24\\n' > "+tab)
	if st := get(); st.OSDaemon == "zfstools" || len(st.CronFiles) != 0 {
		t.Fatalf("status with a commented-out line: %+v, want no zfstools", st)
	}

	vmExec(t, "printf '0 * * * * root /usr/local/sbin/zfs-auto-snapshot hourly 24\\n' > "+tab)
	st := get()
	if st.OSDaemon != "zfstools" || !st.OSDaemonActive || len(st.CronFiles) != 1 || st.CronFiles[0] != tab {
		t.Fatalf("status with a zfstools cron line: %+v, want zfstools active from %s", st, tab)
	}
	before := vmExec(t, "md5 -q "+tab)
	apiStatus(t, http.StatusConflict, "POST", "/api/v1/auto-snapshot/takeover", nil)
	if after := vmExec(t, "md5 -q "+tab); after != before {
		t.Fatalf("takeover changed %s", tab)
	}

	vmExec(t, "rm -f "+tab)
	if st := get(); st.OSDaemon != "zfs-periodic" || len(st.CronFiles) != 0 {
		t.Fatalf("status without the cron line: %+v, want zfs-periodic", st)
	}
}
