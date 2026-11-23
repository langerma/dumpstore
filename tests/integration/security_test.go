//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

// TestSSHKeyFileSafety covers #215/#216: authorized_keys lives in a directory
// the user controls, and dumpstore writes it as root. A symlinked ~/.ssh, a
// symlinked authorized_keys or a hard link to another file must be refused
// (409) for add, list and remove, leaving the root-owned target untouched.
// The links are planted as root here, standing in for the local user.
func TestSSHKeyFileSafety(t *testing.T) {
	const name = "itest-keyfile"
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOKtWwfbM2iF+MtO37AgDseyCwAS8RAERcXAj41LUEHu itest@keyfile"
	createUser(t, name)
	home := strings.TrimSpace(vmExec(t, "eval echo ~"+name))
	if home == "" || home == "~"+name {
		t.Fatalf("no home for %s", name)
	}
	vmExec(t, "mkdir -p "+home+" && chown "+name+":"+name+" "+home)

	// Root-owned decoys next to the home, so a hard link stays on one
	// filesystem.
	parent := home[:strings.LastIndex(home, "/")]
	victim := parent + "/itest-keyfile-victim"
	victimDir := parent + "/itest-keyfile-dir"
	cleanup := func() { _, _ = vmExecErr("rm -rf " + victim + " " + victimDir + " " + home + "/.ssh") }
	cleanup()
	t.Cleanup(cleanup)
	vmExec(t, "printf 'root-only-SECRET\\n' > "+victim+" && chmod 600 "+victim+" && mkdir -m 700 "+victimDir)
	// Mode, owner, group and content of the decoys. Not the link count: the
	// hard-link case raises it itself.
	victimState := func() string {
		return vmExec(t, "ls -ln "+victim+" | awk '{print $1,$3,$4}'; cat "+victim+"; ls -A "+victimDir)
	}
	before := victimState()

	keysURL := v1("/users/%s/ssh-keys", name)
	refuses := func(t *testing.T, what string) {
		t.Helper()
		apiStatus(t, http.StatusConflict, "POST", keysURL, map[string]string{"key": key})
		b := apiStatus(t, http.StatusConflict, "GET", keysURL, nil)
		if strings.Contains(string(b), "SECRET") {
			t.Errorf("%s: listing disclosed the victim's content: %s", what, b)
		}
		apiStatus(t, http.StatusConflict, "DELETE", v1("/users/%s/ssh-keys/%s", name, "SHA256:x"), nil)
		if after := victimState(); after != before {
			t.Errorf("%s: victim changed:\n before %q\n after  %q", what, before, after)
		}
	}

	t.Run("symlinked .ssh", func(t *testing.T) {
		vmExec(t, "rm -rf "+home+"/.ssh && ln -s "+victimDir+" "+home+"/.ssh && chown -h "+name+" "+home+"/.ssh")
		refuses(t, "symlinked .ssh")
	})
	t.Run("symlinked authorized_keys", func(t *testing.T) {
		vmExec(t, "rm -rf "+home+"/.ssh && mkdir -m 700 "+home+"/.ssh && chown "+name+":"+name+" "+home+"/.ssh"+
			" && ln -s "+victim+" "+home+"/.ssh/authorized_keys && chown -h "+name+" "+home+"/.ssh/authorized_keys")
		refuses(t, "symlinked authorized_keys")
	})
	t.Run("hard-linked authorized_keys", func(t *testing.T) {
		vmExec(t, "rm -f "+home+"/.ssh/authorized_keys")
		if _, err := vmExecErr("ln " + victim + " " + home + "/.ssh/authorized_keys"); err != nil {
			t.Skipf("home and %s are on different filesystems: %v", parent, err)
		}
		refuses(t, "hard-linked authorized_keys")
	})
	t.Run("normal add after cleanup", func(t *testing.T) {
		vmExec(t, "rm -rf "+home+"/.ssh")
		assertTasks(t, apiOK(t, "POST", keysURL, map[string]string{"key": key}))
		uid := strings.TrimSpace(vmExec(t, "id -u "+name))
		ls := strings.Fields(vmExec(t, "ls -lnd "+home+"/.ssh "+home+"/.ssh/authorized_keys | awk '{print $1, $3}'"))
		if len(ls) != 4 || ls[0] != "drwx------" || ls[1] != uid || ls[2] != "-rw-------" || ls[3] != uid {
			t.Errorf(".ssh / authorized_keys mode and owner: %v, want drwx------ %s / -rw------- %s", ls, uid, uid)
		}
		if got := strings.TrimSpace(vmExec(t, "cat "+home+"/.ssh/authorized_keys")); got != key {
			t.Errorf("authorized_keys = %q", got)
		}
	})
}

// TestSMBConfInjection covers #217: values rendered into smb.conf must not
// carry a newline (a new parameter or section) or use a reserved section
// name. Each is refused with 400 and smb.conf stays byte-identical.
func TestSMBConfInjection(t *testing.T) {
	if !ensureSMBInit(t) {
		skipMissingTool(t, "smbd not installed in VM %s", vmName)
	}
	conf := decode[struct {
		ConfPath string `json:"conf_path"`
	}](t, apiOK(t, "GET", "/api/v1/smb/status", nil)).ConfPath
	sum := func() string { return vmExec(t, "cksum "+conf) }
	before := sum()

	for name, body := range map[string]map[string]string{
		"path newline":        {"path": "/" + testPool + "/tm\n   root preexec = /bin/true"},
		"path backslash":      {"path": "/" + testPool + "/tm\\"},
		"max_size newline":    {"path": "/" + testPool, "max_size": "1G\n[pwn]"},
		"valid_users newline": {"path": "/" + testPool, "valid_users": "alice\n   path = /"},
	} {
		t.Run(name, func(t *testing.T) {
			apiStatus(t, http.StatusBadRequest, "PUT", v1("/smb/timemachine/%s", "itest-inject"), body)
		})
	}
	for _, reserved := range []string{"homes", "global"} {
		t.Run("reserved name "+reserved, func(t *testing.T) {
			apiStatus(t, http.StatusBadRequest, "PUT", v1("/smb/timemachine/%s", reserved), map[string]string{"path": "/" + testPool})
		})
	}
	t.Run("homes path newline", func(t *testing.T) {
		apiStatus(t, http.StatusBadRequest, "PUT", "/api/v1/smb/homes", map[string]string{"path": "/" + testPool + "/homes/%U\n[pwn]"})
	})
	if after := sum(); after != before {
		t.Errorf("smb.conf changed by refused requests:\n before %s\n after  %s", before, after)
	}
}
