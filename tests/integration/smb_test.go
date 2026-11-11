//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestSMBSurface drives the Samba endpoints: init, usershares, Samba users,
// [homes], and Time Machine shares. smb.conf is fully owned by dumpstore, so
// every write re-renders it and restarts smbd — the VM is a disposable host.
func TestSMBSurface(t *testing.T) {
	skipUnlessVMTool(t, "smbd")

	type smbStatus struct {
		Initialized bool   `json:"initialized"`
		ConfPath    string `json:"conf_path"`
	}
	conf := decode[smbStatus](t, apiOK(t, "GET", "/api/v1/smb/status", nil)).ConfPath

	// Init gate (#193): a distro-default smb.conf (no dumpstore header) is
	// not initialised; init backs it up to .pre-dumpstore and renders ours.
	// The VM's smb.conf and backup are set aside and restored afterwards.
	const distro = "# Sample configuration file for the Samba suite.\n[global]\n   workgroup = ITESTGROUP\n"
	vmExec(t, "for f in "+conf+" "+conf+".pre-dumpstore; do rm -f $f.itest-bak; [ -e $f ] && cp -p $f $f.itest-bak; done; true")
	t.Cleanup(func() {
		_, _ = vmExecErr("for f in " + conf + " " + conf + ".pre-dumpstore; do if [ -e $f.itest-bak ]; then mv $f.itest-bak $f; else rm -f $f; fi; done; smbcontrol all reload-config >/dev/null 2>&1; true")
	})
	vmExec(t, "rm -f "+conf+".pre-dumpstore; printf '"+distro+"' > "+conf)
	if decode[smbStatus](t, apiOK(t, "GET", "/api/v1/smb/status", nil)).Initialized {
		t.Fatalf("distro-default %s counted as initialized", conf)
	}
	apiStatus(t, http.StatusConflict, "GET", "/api/v1/smb/shares", nil)

	assertTasks(t, apiOK(t, "POST", "/api/v1/smb/init", nil))
	if !decode[smbStatus](t, apiOK(t, "GET", "/api/v1/smb/status", nil)).Initialized {
		t.Fatalf("smb not initialized after POST /api/v1/smb/init")
	}
	if got := vmExec(t, "cat "+conf+".pre-dumpstore"); strings.TrimSpace(got) != strings.TrimSpace(distro) {
		t.Fatalf("%s.pre-dumpstore = %q, want the distro file", conf, got)
	}
	if got := vmExec(t, "grep -c 'workgroup = ITESTGROUP' "+conf); strings.TrimSpace(got) != "1" {
		t.Fatalf("init did not carry the workgroup over into %s", conf)
	}
	smbdPID := smbdMainPID(t)

	t.Run("usershare", func(t *testing.T) {
		ds := testPool + "/itest-smb"
		const share = "itest-share"
		mp := createDataset(t, ds)
		t.Cleanup(func() { _, _ = vmExecErr("net usershare delete " + share + " 2>/dev/null; true") })

		assertTasks(t, apiOK(t, "PUT", v1("/smb/shares/%s", share), map[string]string{"dataset": ds}))
		if !hasUsershare(t, share) {
			t.Fatalf("share %s not listed after create", share)
		}
		b := apiOK(t, "DELETE", v1("/smb/shares/%s", share), nil)
		assertTasks(t, b)
		if r := decode[struct {
			Sharename string `json:"sharename"`
			Path      string `json:"path"`
		}](t, b); r.Sharename != share || r.Path != mp {
			t.Fatalf("delete response %+v, want sharename %s path %s", r, share, mp)
		}
		if hasUsershare(t, share) {
			t.Fatalf("share %s still listed after delete", share)
		}
		apiStatus(t, http.StatusNotFound, "DELETE", v1("/smb/shares/%s", share), nil)
		apiStatus(t, http.StatusBadRequest, "PUT", v1("/smb/shares/%s", "bad share!"), map[string]string{"dataset": ds})
		apiStatus(t, http.StatusBadRequest, "PUT", v1("/smb/shares/%s", share), map[string]string{})
	})

	t.Run("users", func(t *testing.T) {
		const user = "itest-smbuser"
		createUser(t, user)
		smbUsers := func() []string {
			r := decode[struct {
				Available bool     `json:"available"`
				Users     []string `json:"users"`
			}](t, apiOK(t, "GET", "/api/v1/smb/users", nil))
			if !r.Available {
				t.Fatalf("smb-users reports pdbedit unavailable")
			}
			return r.Users
		}
		assertTasks(t, apiOK(t, "PUT", v1("/smb/users/%s", user), map[string]string{"password": "itest-smb-pw"}))
		if !slices.Contains(smbUsers(), user) {
			t.Fatalf("%s not registered in Samba after add", user)
		}
		assertTasks(t, apiOK(t, "DELETE", v1("/smb/users/%s", user), nil))
		if slices.Contains(smbUsers(), user) {
			t.Fatalf("%s still registered in Samba after remove", user)
		}
		apiStatus(t, http.StatusBadRequest, "PUT", v1("/smb/users/%s", user), map[string]string{})
	})

	t.Run("homes", func(t *testing.T) {
		ds := testPool + "/itest-homes"
		createDataset(t, ds)
		t.Cleanup(func() {
			if status, _ := api(t, "DELETE", "/api/v1/smb/homes", nil); status != http.StatusOK {
				t.Logf("homes cleanup: DELETE returned %d", status)
			}
		})
		assertTasks(t, apiOK(t, "PUT", "/api/v1/smb/homes", map[string]string{"dataset": ds, "browseable": "yes"}))
		h := decode[struct {
			Enabled    bool   `json:"enabled"`
			Path       string `json:"path"`
			Browseable string `json:"browseable"`
		}](t, apiOK(t, "GET", "/api/v1/smb/homes", nil))
		if !h.Enabled || h.Browseable != "yes" || h.Path == "" {
			t.Fatalf("homes after enable: %+v", h)
		}
		assertTasks(t, apiOK(t, "DELETE", "/api/v1/smb/homes", nil))
		if decode[map[string]any](t, apiOK(t, "GET", "/api/v1/smb/homes", nil))["enabled"] != false {
			t.Fatalf("homes still enabled after delete")
		}
		apiStatus(t, http.StatusBadRequest, "PUT", "/api/v1/smb/homes", map[string]string{"dataset": ds, "browseable": "maybe"})
	})

	t.Run("timemachine", func(t *testing.T) {
		ds := testPool + "/itest-tm"
		const share = "itest-tm"
		createDataset(t, ds)
		t.Cleanup(func() { _, _ = api(t, "DELETE", v1("/smb/timemachine/%s", share), nil) })

		assertTasks(t, apiOK(t, "PUT", v1("/smb/timemachine/%s", share), map[string]string{
			"dataset": ds, "max_size": "10G",
		}))
		if !hasTimeMachine(t, share) {
			t.Fatalf("Time Machine share %s not listed after create", share)
		}
		assertTasks(t, apiOK(t, "DELETE", v1("/smb/timemachine/%s", share), nil))
		if hasTimeMachine(t, share) {
			t.Fatalf("Time Machine share %s still listed after delete", share)
		}
		apiStatus(t, http.StatusBadRequest, "PUT", v1("/smb/timemachine/%s", "bad share!"), map[string]string{"dataset": ds})
	})

	// Every write above re-rendered smb.conf. Samba must have been reloaded,
	// not restarted: a restart drops clients, and a few in quick succession
	// trip systemd's start limit and leave smbd failed.
	if smbdPID != "" {
		if after := smbdMainPID(t); after != smbdPID {
			t.Fatalf("smbd restarted by config writes: MainPID %s → %s", smbdPID, after)
		}
	}
}

// smbdMainPID returns smbd's systemd MainPID, or "" on non-systemd hosts.
func smbdMainPID(t *testing.T) string {
	t.Helper()
	if _, err := vmExecErr("command -v systemctl"); err != nil {
		return ""
	}
	pid := strings.TrimSpace(vmExec(t, "systemctl show -p MainPID --value smbd"))
	if pid == "" || pid == "0" {
		t.Fatalf("smbd not running (MainPID %q)", pid)
	}
	return pid
}

func hasUsershare(t *testing.T, name string) bool {
	t.Helper()
	// null when no usershares exist.
	for _, s := range decode[[]struct {
		Name string `json:"name"`
	}](t, apiOK(t, "GET", "/api/v1/smb/shares", nil)) {
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
	}](t, apiOK(t, "GET", "/api/v1/smb/timemachine", nil)) {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestSMBWriteWhileStopped: a config write must succeed while Samba is
// stopped and leave it running (#196). On FreeBSD `service samba_server
// reload` fails when Samba isn't running, so the apply playbook starts
// stopped services instead of reloading them.
func TestSMBWriteWhileStopped(t *testing.T) {
	skipUnlessVMTool(t, "smbd")
	assertTasks(t, apiOK(t, "POST", "/api/v1/smb/init", nil))

	stop := "systemctl stop smbd nmbd"
	running := "systemctl is-active --quiet smbd"
	if _, err := vmExecErr("command -v systemctl"); err != nil {
		stop = "service samba_server stop || service samba_server onestop"
		running = "service samba_server status"
	}
	// Leave Samba running for whatever runs next, whatever happens here.
	t.Cleanup(func() {
		if _, err := vmExecErr(running); err != nil {
			_, _ = vmExecErr("systemctl start smbd nmbd 2>/dev/null || service samba_server start || service samba_server onestart")
		}
	})

	vmExec(t, stop)
	if _, err := vmExecErr(running); err == nil {
		t.Fatal("Samba still running after stop — test setup failed")
	}

	ds := testPool + "/itest-tm-stopped"
	const share = "itest-tm-stopped"
	createDataset(t, ds)
	t.Cleanup(func() { _, _ = api(t, "DELETE", v1("/smb/timemachine/%s", share), nil) })
	assertTasks(t, apiOK(t, "PUT", v1("/smb/timemachine/%s", share), map[string]string{"dataset": ds}))

	if _, err := vmExecErr(running); err != nil {
		t.Fatalf("Samba not running after a config write while it was stopped: %v", err)
	}
}

// ensureSMBInit initialises Samba for tests that need the init-gated SMB
// routes (#193) without depending on an earlier test having done it: a fresh
// CI VM ships the distro smb.conf, which is not initialised. When it has to
// initialise, the VM's smb.conf and any .pre-dumpstore backup are set aside
// first and restored on cleanup, so the host is left as it was. Returns false
// when Samba is not installed.
func ensureSMBInit(t *testing.T) bool {
	t.Helper()
	if _, err := vmExecErr("command -v smbd"); err != nil {
		return false
	}
	st := decode[struct {
		Initialized bool   `json:"initialized"`
		ConfPath    string `json:"conf_path"`
	}](t, apiOK(t, "GET", "/api/v1/smb/status", nil))
	if st.Initialized {
		return true
	}
	conf := st.ConfPath
	vmExec(t, "for f in "+conf+" "+conf+".pre-dumpstore; do rm -f $f.itest-bak; [ -e $f ] && cp -p $f $f.itest-bak; done; true")
	t.Cleanup(func() {
		_, _ = vmExecErr("for f in " + conf + " " + conf + ".pre-dumpstore; do if [ -e $f.itest-bak ]; then mv $f.itest-bak $f; else rm -f $f; fi; done; smbcontrol all reload-config >/dev/null 2>&1; true")
	})
	assertTasks(t, apiOK(t, "POST", "/api/v1/smb/init", nil))
	return true
}
