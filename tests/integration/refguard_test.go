//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// refConflictBody is the 409 body of the dataset reference guard (#183).
type refConflictBody struct {
	Error     string `json:"error"`
	Conflicts []struct {
		Kind     string `json:"kind"`
		Dataset  string `json:"dataset"`
		Name     string `json:"name"`
		Describe string `json:"describe"`
	} `json:"conflicts"`
}

// assertBlocked expects a 409 from the reference guard that names a conflict
// of kind for name, and checks that dataset still exists afterwards.
func assertBlocked(t *testing.T, method, path string, body any, dataset, kind, name string) {
	t.Helper()
	r := decode[refConflictBody](t, apiStatus(t, http.StatusConflict, method, path, body))
	found := false
	for _, c := range r.Conflicts {
		found = found || (c.Kind == kind && c.Name == name && c.Describe != "")
	}
	if !found {
		t.Fatalf("%s %s: 409 without a %s conflict for %q: %+v", method, path, kind, name, r)
	}
	if _, ok := datasetByName(t, dataset); !ok {
		t.Fatalf("%s %s was refused but %s is gone", method, path, dataset)
	}
}

// TestDatasetRefGuard covers #183: destroying, renaming, or re-mounting a
// dataset that dumpstore-managed config still references is refused with a
// 409 listing the blockers; once the reference is removed it succeeds.
func TestDatasetRefGuard(t *testing.T) {
	t.Run("usershare", func(t *testing.T) {
		skipUnlessVMTool(t, "smbd")
		assertTasks(t, apiOK(t, "POST", "/api/smb/init", nil))

		parent := testPool + "/itest-ref"
		ds := parent + "/shared"
		const share = "itest-refshare"
		createDataset(t, parent)
		mp := createDataset(t, ds)
		t.Cleanup(func() { _, _ = vmExecErr("net usershare delete " + share + " 2>/dev/null; true") })
		assertTasks(t, apiOK(t, "POST", "/api/smb-share/"+ds, map[string]string{"sharename": share}))

		assertBlocked(t, "DELETE", "/api/datasets/"+ds, nil, ds, "smb_usershare", share)
		// A recursive delete of the parent covers the shared child.
		assertBlocked(t, "DELETE", "/api/datasets/"+parent+"?recursive=true", nil, ds, "smb_usershare", share)
		assertBlocked(t, "POST", "/api/datasets/rename", map[string]string{"name": ds, "new_name": parent + "/moved"}, ds, "smb_usershare", share)
		assertBlocked(t, "PATCH", "/api/datasets/"+ds, map[string]string{"mountpoint": mp + "-moved"}, ds, "smb_usershare", share)

		assertTasks(t, apiOK(t, "DELETE", "/api/smb-share/"+ds+"?name="+share, nil))
		apiOK(t, "DELETE", "/api/datasets/"+ds, nil)
		if _, ok := datasetByName(t, ds); ok {
			t.Fatalf("%s still exists after unreferenced delete", ds)
		}
	})

	// A usershare whose dataset vanished outside dumpstore (or before the
	// guard existed) must be visible and removable, so it cannot silently
	// re-activate — with its old ACL — on a new dataset at the same path.
	t.Run("stale usershare", func(t *testing.T) {
		skipUnlessVMTool(t, "smbd")
		assertTasks(t, apiOK(t, "POST", "/api/smb/init", nil))

		ds := testPool + "/itest-ref-stale"
		const share = "itest-stale"
		mp := createDataset(t, ds)
		t.Cleanup(func() { _, _ = vmExecErr("net usershare delete " + share + " 2>/dev/null; true") })
		assertTasks(t, apiOK(t, "POST", "/api/smb-share/"+ds, map[string]string{"sharename": share}))

		destroyDatasetInVM(ds) // bypass the guard, as a pre-#183 delete did
		stale := false
		for _, s := range decode[[]struct {
			Name        string `json:"name"`
			Path        string `json:"path"`
			PathMissing bool   `json:"path_missing"`
		}](t, apiOK(t, "GET", "/api/smb-shares", nil)) {
			stale = stale || (s.Name == share && s.Path == mp && s.PathMissing)
		}
		if !stale {
			t.Fatalf("stale share %s (%s) not listed with path_missing", share, mp)
		}

		// The UI's Remove button for stale shares sends the path as the
		// (audit-only) dataset segment.
		assertTasks(t, apiOK(t, "DELETE", "/api/smb-share/"+strings.TrimPrefix(mp, "/")+"?name="+share, nil))
		if hasUsershare(t, share) {
			t.Fatalf("stale share %s still listed after remove", share)
		}

		// Recreating a dataset at the same path must not bring the share back.
		createDataset(t, ds)
		if hasUsershare(t, share) {
			t.Fatalf("share %s reappeared on a new dataset at %s", share, mp)
		}
		if out := vmExec(t, "net usershare list"); strings.Contains(out, share) {
			t.Fatalf("Samba still serves %s on the new dataset: %s", share, out)
		}
	})

	t.Run("replication", func(t *testing.T) {
		src := testPool + "/itest-ref-src"
		dst := testPool + "/itest-ref-dst"
		t.Cleanup(func() { deleteITestReplTasks(t) })
		deleteITestReplTasks(t)
		createDataset(t, src)
		createDataset(t, dst)

		task := decode[replTask](t, apiStatus(t, http.StatusCreated, "POST", "/api/replication", map[string]any{
			"name": "itest-ref-repl", "source": src, "target": dst, "schedule": "0 3 * * *",
		}))
		assertBlocked(t, "DELETE", "/api/datasets/"+src, nil, src, "replication_source", task.Name)
		assertBlocked(t, "DELETE", "/api/datasets/"+dst, nil, dst, "replication_target", task.Name)

		apiStatus(t, http.StatusNoContent, "DELETE", "/api/replication/"+task.ID, nil)
		apiOK(t, "DELETE", "/api/datasets/"+src, nil)
		apiOK(t, "DELETE", "/api/datasets/"+dst, nil)
	})

	t.Run("iscsi", func(t *testing.T) {
		zvol := testPool + "/itest-ref-vol"
		const iqn = "iqn.2026-11.local.dumpstore:itest-ref"
		destroyDatasetInVM(zvol)
		iscsiFixture(t, zvol, iqn)
		t.Cleanup(func() { destroyDatasetInVM(zvol) })

		apiOK(t, "POST", "/api/datasets", map[string]any{"name": zvol, "type": "volume", "volsize": "64M"})
		waitFor(t, "zvol device node", 30*time.Second, func() bool {
			_, err := vmExecErr("test -e /dev/zvol/" + zvol)
			return err == nil
		})
		assertTasks(t, apiOK(t, "POST", "/api/iscsi-targets", map[string]any{
			"zvol": zvol, "iqn": iqn, "auth_mode": "none",
		}))

		// Previously a generic 500 from `zfs destroy`; now a 409 naming the target.
		assertBlocked(t, "DELETE", "/api/datasets/"+zvol, nil, zvol, "iscsi_target", iqn)

		q := "?iqn=" + url.QueryEscape(iqn) + "&zvol=" + url.QueryEscape(zvol)
		assertTasks(t, apiOK(t, "DELETE", "/api/iscsi-targets"+q, nil))
		apiOK(t, "DELETE", "/api/datasets/"+zvol, nil)
	})

	// An unreadable consumer config aborts the delete instead of skipping
	// the check. Root ignores file modes, so smb.conf is swapped for a
	// directory — reading it then fails with EISDIR.
	t.Run("fail closed", func(t *testing.T) {
		skipUnlessVMTool(t, "smbd")
		assertTasks(t, apiOK(t, "POST", "/api/smb/init", nil))
		conf := "/etc/samba/smb.conf"
		if out, _ := vmExecErr("uname -s"); strings.TrimSpace(out) == "FreeBSD" {
			conf = "/usr/local/etc/smb4.conf"
		}
		vmExec(t, "test -f "+conf) // just rendered by init
		ds := testPool + "/itest-ref-closed"
		createDataset(t, ds)

		vmExec(t, "mv "+conf+" "+conf+".itest-bak && mkdir "+conf)
		t.Cleanup(func() {
			_, _ = vmExecErr("test -f " + conf + ".itest-bak && rmdir " + conf + " && mv " + conf + ".itest-bak " + conf)
		})

		b := apiStatus(t, http.StatusInternalServerError, "DELETE", "/api/datasets/"+ds, nil)
		if !strings.Contains(string(b), "reference check failed") {
			t.Fatalf("fail-closed delete: unexpected error body %s", truncate(b, 500))
		}
		if _, ok := datasetByName(t, ds); !ok {
			t.Fatalf("%s destroyed although the reference check failed", ds)
		}
	})
}

// iscsiFixture clears any stale target for iqn/zvol in the VM, skips when no
// iSCSI backend is installed, and registers cleanup that leaves the host as
// it was. Linux: targetcli/LIO. FreeBSD: ctld — /etc/ctl.conf, ctld_enable,
// and whether ctld was running are restored.
func iscsiFixture(t *testing.T, zvol, iqn string) {
	t.Helper()
	if _, err := vmExecErr("command -v targetcli"); err == nil {
		backstore := strings.ReplaceAll(zvol, "/", "-")
		cleanup := func() {
			_, _ = vmExecErr("targetcli /iscsi delete " + iqn + " >/dev/null 2>&1;" +
				" targetcli /backstores/block delete " + backstore + " >/dev/null 2>&1;" +
				" targetcli saveconfig >/dev/null 2>&1; true")
		}
		cleanup()
		t.Cleanup(cleanup)
		return
	}
	if _, err := vmExecErr("command -v ctld"); err != nil {
		t.Skipf("no iSCSI backend (targetcli or ctld) in VM %s", vmName)
	}
	// Service commands redirect stdio: a daemon started over `limactl shell`
	// otherwise inherits the session's fds and the call never returns.
	const bak = "/etc/ctl.conf.itest-bak"
	vmExec(t, "rm -rf "+bak+"*; "+
		"if [ -f /etc/ctl.conf ]; then cp -p /etc/ctl.conf "+bak+"; fi; "+
		"sysrc -n ctld_enable > "+bak+".enable; "+
		"if service ctld onestatus >/dev/null 2>&1; then touch "+bak+".running; fi")
	t.Cleanup(func() {
		_, _ = vmExecErr("if [ -f " + bak + " ]; then mv " + bak + " /etc/ctl.conf; else rm -f /etc/ctl.conf; fi; " +
			"sysrc ctld_enable=$(cat " + bak + ".enable) >/dev/null; " +
			"if [ -f " + bak + ".running ]; then service ctld onereload >/dev/null 2>&1 </dev/null; " +
			"else service ctld onestop >/dev/null 2>&1 </dev/null; fi; " +
			"rm -f " + bak + ".enable " + bak + ".running; true")
	})
}
