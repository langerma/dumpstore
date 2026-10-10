//go:build integration

package integration

import (
	"net/http"
	"slices"
	"testing"
)

// TestSMBSurface drives the Samba endpoints: init, usershares, Samba users,
// [homes], and Time Machine shares. smb.conf is fully owned by dumpstore, so
// every write re-renders it and restarts smbd — the VM is a disposable host.
func TestSMBSurface(t *testing.T) {
	skipUnlessVMTool(t, "smbd")

	status := decode[struct {
		Initialized bool `json:"initialized"`
	}](t, apiOK(t, "GET", "/api/smb/status", nil))
	if !status.Initialized {
		// Init gate: share writes are refused until smb.conf exists. Debian's
		// samba package ships one, so this only runs on hosts without it.
		apiStatus(t, http.StatusConflict, "GET", "/api/smb-shares", nil)
	}
	assertTasks(t, apiOK(t, "POST", "/api/smb/init", nil))
	if !decode[struct {
		Initialized bool `json:"initialized"`
	}](t, apiOK(t, "GET", "/api/smb/status", nil)).Initialized {
		t.Fatalf("smb not initialized after POST /api/smb/init")
	}

	t.Run("usershare", func(t *testing.T) {
		ds := testPool + "/itest-smb"
		const share = "itest-share"
		createDataset(t, ds)
		t.Cleanup(func() { _, _ = vmExecErr("net usershare delete " + share + " 2>/dev/null; true") })

		assertTasks(t, apiOK(t, "POST", "/api/smb-share/"+ds, map[string]string{"sharename": share}))
		if !hasUsershare(t, share) {
			t.Fatalf("share %s not listed after create", share)
		}
		assertTasks(t, apiOK(t, "DELETE", "/api/smb-share/"+ds+"?name="+share, nil))
		if hasUsershare(t, share) {
			t.Fatalf("share %s still listed after delete", share)
		}
		apiStatus(t, http.StatusBadRequest, "POST", "/api/smb-share/"+ds, map[string]string{"sharename": "bad share!"})
		apiStatus(t, http.StatusBadRequest, "DELETE", "/api/smb-share/"+ds, nil)
	})

	t.Run("users", func(t *testing.T) {
		const user = "itest-smbuser"
		createUser(t, user)
		smbUsers := func() []string {
			r := decode[struct {
				Available bool     `json:"available"`
				Users     []string `json:"users"`
			}](t, apiOK(t, "GET", "/api/smb-users", nil))
			if !r.Available {
				t.Fatalf("smb-users reports pdbedit unavailable")
			}
			return r.Users
		}
		assertTasks(t, apiOK(t, "POST", "/api/smb-users/"+user, map[string]string{"password": "itest-smb-pw"}))
		if !slices.Contains(smbUsers(), user) {
			t.Fatalf("%s not registered in Samba after add", user)
		}
		assertTasks(t, apiOK(t, "DELETE", "/api/smb-users/"+user, nil))
		if slices.Contains(smbUsers(), user) {
			t.Fatalf("%s still registered in Samba after remove", user)
		}
		apiStatus(t, http.StatusBadRequest, "POST", "/api/smb-users/"+user, map[string]string{})
	})

	t.Run("homes", func(t *testing.T) {
		ds := testPool + "/itest-homes"
		createDataset(t, ds)
		t.Cleanup(func() {
			if status, _ := api(t, "DELETE", "/api/smb/homes", nil); status != http.StatusOK {
				t.Logf("homes cleanup: DELETE returned %d", status)
			}
		})
		assertTasks(t, apiOK(t, "POST", "/api/smb/homes", map[string]string{"dataset": ds, "browseable": "yes"}))
		h := decode[struct {
			Enabled    bool   `json:"enabled"`
			Path       string `json:"path"`
			Browseable string `json:"browseable"`
		}](t, apiOK(t, "GET", "/api/smb/homes", nil))
		if !h.Enabled || h.Browseable != "yes" || h.Path == "" {
			t.Fatalf("homes after enable: %+v", h)
		}
		assertTasks(t, apiOK(t, "DELETE", "/api/smb/homes", nil))
		if decode[map[string]any](t, apiOK(t, "GET", "/api/smb/homes", nil))["enabled"] != false {
			t.Fatalf("homes still enabled after delete")
		}
		apiStatus(t, http.StatusBadRequest, "POST", "/api/smb/homes", map[string]string{"dataset": ds, "browseable": "maybe"})
	})

	t.Run("timemachine", func(t *testing.T) {
		ds := testPool + "/itest-tm"
		const share = "itest-tm"
		createDataset(t, ds)
		t.Cleanup(func() { _, _ = api(t, "DELETE", "/api/smb/timemachine/"+share, nil) })

		assertTasks(t, apiOK(t, "POST", "/api/smb/timemachine", map[string]string{
			"sharename": share, "dataset": ds, "max_size": "10G",
		}))
		if !hasTimeMachine(t, share) {
			t.Fatalf("Time Machine share %s not listed after create", share)
		}
		assertTasks(t, apiOK(t, "DELETE", "/api/smb/timemachine/"+share, nil))
		if hasTimeMachine(t, share) {
			t.Fatalf("Time Machine share %s still listed after delete", share)
		}
		apiStatus(t, http.StatusBadRequest, "POST", "/api/smb/timemachine", map[string]string{"dataset": ds})
	})
}

func hasUsershare(t *testing.T, name string) bool {
	t.Helper()
	// null when no usershares exist.
	for _, s := range decode[[]struct {
		Name string `json:"name"`
	}](t, apiOK(t, "GET", "/api/smb-shares", nil)) {
		if s.Name == name {
			return true
		}
	}
	return false
}

func hasTimeMachine(t *testing.T, name string) bool {
	t.Helper()
	for _, s := range decode[[]struct {
		Name string `json:"name"`
	}](t, apiOK(t, "GET", "/api/smb/timemachine", nil)) {
		if s.Name == name {
			return true
		}
	}
	return false
}
