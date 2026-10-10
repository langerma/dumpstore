//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type aclEntry struct {
	Tag       string `json:"tag"`
	Qualifier string `json:"qualifier"`
	Perms     string `json:"perms"`
	Default   bool   `json:"default"`
}

// TestPosixACL sets, reads, and removes a POSIX ACL entry on a dataset
// mountpoint, then checks the 404 on removing it twice.
func TestPosixACL(t *testing.T) {
	if out, _ := vmExecErr("uname -s"); strings.TrimSpace(out) != "Linux" {
		t.Skip("POSIX ACLs are Linux-only; FreeBSD datasets use NFSv4 ACLs (TestNFSv4ACL)")
	}
	skipUnlessVMTool(t, "setfacl")
	ds := testPool + "/itest-acl"
	const user = "itest-acluser"
	createDataset(t, ds)
	createUser(t, user)

	// acltype=off (the Linux default) is refused until enabled.
	apiStatus(t, http.StatusBadRequest, "POST", "/api/acl/"+ds, map[string]string{"ace": "user:" + user + ":rwx"})
	apiOK(t, "PATCH", "/api/datasets/"+ds, map[string]string{"acltype": "posix"})

	if !decode[map[string]bool](t, apiOK(t, "GET", "/api/acl-status", nil))[ds] {
		t.Errorf("acl-status does not report ACLs on %s after acltype=posix", ds)
	}

	assertTasks(t, apiOK(t, "POST", "/api/acl/"+ds, map[string]string{"ace": "user:" + user + ":rwx"}))
	acl := decode[struct {
		ACLType string     `json:"acl_type"`
		Entries []aclEntry `json:"entries"`
	}](t, apiOK(t, "GET", "/api/acl/"+ds, nil))
	if acl.ACLType != "posix" || !hasACE(acl.Entries, user) {
		t.Fatalf("ACL after set: type %q, entries %+v; want posix with user:%s", acl.ACLType, acl.Entries, user)
	}
	if out := vmExec(t, "getfacl -cp $(zfs get -H -o value mountpoint "+ds+")"); !strings.Contains(out, "user:"+user+":rwx") {
		t.Fatalf("getfacl does not show the entry: %s", out)
	}

	entry := url.QueryEscape("user:" + user)
	assertTasks(t, apiOK(t, "DELETE", "/api/acl/"+ds+"?entry="+entry, nil))
	if acl := decode[struct {
		Entries []aclEntry `json:"entries"`
	}](t, apiOK(t, "GET", "/api/acl/"+ds, nil)); hasACE(acl.Entries, user) {
		t.Fatalf("entry still present after delete: %+v", acl.Entries)
	}
	apiStatus(t, http.StatusNotFound, "DELETE", "/api/acl/"+ds+"?entry="+entry, nil)

	apiStatus(t, http.StatusBadRequest, "POST", "/api/acl/"+ds, map[string]string{"ace": "user:x y:rwx"})
	apiStatus(t, http.StatusBadRequest, "DELETE", "/api/acl/"+ds, nil)
	apiStatus(t, http.StatusNotFound, "POST", "/api/acl/"+testPool+"/itest-nonexistent", map[string]string{"ace": "user:root:r"})
}

// TestNFSv4ACL adds and removes an NFSv4 ACE through the API's
// nfs4-acl-tools entry form. FreeBSD only: its base setfacl applies NFSv4
// ACLs to ZFS natively, while Linux ZFS cannot apply them to a local mount.
func TestNFSv4ACL(t *testing.T) {
	if out, _ := vmExecErr("uname -s"); strings.TrimSpace(out) != "FreeBSD" {
		t.Skip("NFSv4 ACLs on local ZFS are FreeBSD-only")
	}
	ds := testPool + "/itest-nfs4acl"
	const user = "itest-nfsuser"
	mp := createDataset(t, ds)
	createUser(t, user)
	apiOK(t, "PATCH", "/api/datasets/"+ds, map[string]string{"acltype": "nfsv4"})

	type nfs4ACL struct {
		ACLType string `json:"acl_type"`
		Entries []struct {
			Tag       string `json:"tag"`
			Flags     string `json:"flags"`
			Qualifier string `json:"qualifier"`
			Perms     string `json:"perms"`
		} `json:"entries"`
	}
	find := func(a nfs4ACL) (flags, perms string, ok bool) {
		for _, e := range a.Entries {
			if e.Tag == "A" && e.Qualifier == user {
				return e.Flags, e.Perms, true
			}
		}
		return "", "", false
	}
	acl := decode[nfs4ACL](t, apiOK(t, "GET", "/api/acl/"+ds, nil))
	if acl.ACLType != "nfsv4" || len(acl.Entries) == 0 {
		t.Fatalf("fresh ACL: %+v, want nfsv4 with owner@/group@/everyone@", acl)
	}

	assertTasks(t, apiOK(t, "POST", "/api/acl/"+ds, map[string]string{"ace": "A:fd:" + user + ":rwx"}))
	flags, perms, ok := find(decode[nfs4ACL](t, apiOK(t, "GET", "/api/acl/"+ds, nil)))
	if !ok || flags != "fd" || perms != "rwx" {
		t.Fatalf("entry after add: flags %q perms %q (found %v), want fd/rwx", flags, perms, ok)
	}
	if out := vmExec(t, "getfacl -q "+mp); !strings.Contains(out, "user:"+user+":rwx") {
		t.Fatalf("getfacl does not show the entry:\n%s", out)
	}

	entry := url.QueryEscape("A:fd:" + user + ":rwx")
	assertTasks(t, apiOK(t, "DELETE", "/api/acl/"+ds+"?entry="+entry, nil))
	if _, _, ok := find(decode[nfs4ACL](t, apiOK(t, "GET", "/api/acl/"+ds, nil))); ok {
		t.Fatalf("entry still present after delete")
	}
	apiStatus(t, http.StatusNotFound, "DELETE", "/api/acl/"+ds+"?entry="+entry, nil)
	apiStatus(t, http.StatusBadRequest, "POST", "/api/acl/"+ds, map[string]string{"ace": "A::" + user + ":rwq"})
}

func hasACE(entries []aclEntry, user string) bool {
	for _, e := range entries {
		if e.Tag == "user" && e.Qualifier == user && e.Perms == "rwx" && !e.Default {
			return true
		}
	}
	return false
}

// TestChown changes a dataset mountpoint's owner/group and verifies it via
// the API and stat in the VM.
func TestChown(t *testing.T) {
	ds := testPool + "/itest-chown"
	const user = "itest-owner"
	mp := createDataset(t, ds)
	createUser(t, user)

	type owner struct {
		Owner string `json:"owner"`
		Group string `json:"group"`
	}
	if o := decode[owner](t, apiOK(t, "GET", "/api/chown/"+ds, nil)); o.Owner != "root" {
		t.Fatalf("fresh dataset owner %q, want root", o.Owner)
	}

	assertTasks(t, apiOK(t, "POST", "/api/chown/"+ds, map[string]any{"owner": user, "group": user}))
	if o := decode[owner](t, apiOK(t, "GET", "/api/chown/"+ds, nil)); o.Owner != user || o.Group != user {
		t.Fatalf("owner after chown: %+v, want %s:%s", o, user, user)
	}
	// GNU stat on Linux, BSD stat on FreeBSD.
	if out := vmExec(t, "stat -c '%U:%G' "+mp+" 2>/dev/null || stat -f '%Su:%Sg' "+mp); strings.TrimSpace(out) != user+":"+user {
		t.Fatalf("stat after chown: %q, want %s:%s", out, user, user)
	}

	apiStatus(t, http.StatusBadRequest, "POST", "/api/chown/"+ds, map[string]any{"owner": user})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/chown/"+ds, map[string]any{"owner": "bad name", "group": user})
	apiStatus(t, http.StatusNotFound, "POST", "/api/chown/"+testPool+"/itest-nonexistent", map[string]any{"owner": user, "group": user})
}
