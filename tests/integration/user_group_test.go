//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

type sysUser struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	Shell    string `json:"shell"`
}

type sysGroup struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

func userByName(t *testing.T, name string) (sysUser, bool) {
	t.Helper()
	for _, u := range decode[[]sysUser](t, apiOK(t, "GET", "/api/users", nil)) {
		if u.Username == name {
			return u, true
		}
	}
	return sysUser{}, false
}

func groupByName(t *testing.T, name string) (sysGroup, bool) {
	t.Helper()
	for _, g := range decode[[]sysGroup](t, apiOK(t, "GET", "/api/groups", nil)) {
		if g.Name == name {
			return g, true
		}
	}
	return sysGroup{}, false
}

// TestUserLifecycle creates a user, manages its SSH keys, modifies it, and
// deletes it — verifying /etc/passwd state via the API and `id` in the VM.
func TestUserLifecycle(t *testing.T) {
	const name = "itest-user"
	const extra = "itest-extra"
	removeGroupInVM(extra)
	t.Cleanup(func() { removeGroupInVM(extra) })

	createUser(t, name)
	u, ok := userByName(t, name)
	if !ok || u.Shell != "/bin/sh" {
		t.Fatalf("user after create: %+v (found %v), want shell /bin/sh", u, ok)
	}
	vmExec(t, "id "+name)

	// SSH keys: empty → add → listed → remove.
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIItestKeyForDumpstoreIntegrationTests itest@host"
	keys := func() []string {
		return decode[struct {
			Keys []string `json:"keys"`
		}](t, apiOK(t, "GET", "/api/users/"+name+"/sshkeys", nil)).Keys
	}
	if k := keys(); len(k) != 0 {
		t.Fatalf("fresh user has keys: %v", k)
	}
	assertTasks(t, apiOK(t, "POST", "/api/users/"+name+"/sshkeys", map[string]string{"key": key}))
	if !slices.Contains(keys(), key) {
		t.Fatalf("key not listed after add: %v", keys())
	}
	assertTasks(t, apiOK(t, "DELETE", "/api/users/"+name+"/sshkeys", map[string]string{"key": key}))
	if slices.Contains(keys(), key) {
		t.Fatalf("key still listed after delete")
	}
	apiStatus(t, http.StatusBadRequest, "POST", "/api/users/"+name+"/sshkeys", map[string]string{"key": "not-a-key"})

	// Modify: shell + supplementary group.
	apiStatus(t, http.StatusCreated, "POST", "/api/groups", map[string]string{"groupname": extra})
	assertTasks(t, apiOK(t, "PUT", "/api/users/"+name, map[string]string{
		"shell": "/bin/bash", "user_groups": extra,
	}))
	if u, _ := userByName(t, name); u.Shell != "/bin/bash" {
		t.Errorf("shell after modify: %q, want /bin/bash", u.Shell)
	}
	if out := vmExec(t, "id -nG "+name); !strings.Contains(out, extra) {
		t.Errorf("user not in %s after modify: %q", extra, out)
	}

	assertTasks(t, apiOK(t, "DELETE", "/api/users/"+name, nil))
	if _, ok := userByName(t, name); ok {
		t.Fatalf("user still present after delete")
	}
	apiStatus(t, http.StatusNotFound, "DELETE", "/api/users/"+name, nil)
}

// TestUserNegatives covers the guards: invalid names, system users, missing users.
func TestUserNegatives(t *testing.T) {
	apiStatus(t, http.StatusBadRequest, "POST", "/api/users", map[string]string{"username": "bad name"})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/users", map[string]string{"username": ""})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/users", map[string]string{"username": "itest-x", "shell": "bin/sh"})
	// daemon (uid 1 on Linux and FreeBSD) is a system user.
	apiStatus(t, http.StatusForbidden, "DELETE", "/api/users/daemon", nil)
	apiStatus(t, http.StatusForbidden, "PUT", "/api/users/daemon", map[string]string{"shell": "/bin/sh"})
	apiStatus(t, http.StatusNotFound, "PUT", "/api/users/itest-nonexistent", map[string]string{"shell": "/bin/sh"})
	apiStatus(t, http.StatusNotFound, "GET", "/api/users/itest-nonexistent/sshkeys", nil)
}

// TestGroupLifecycle creates, modifies (members, rename), and deletes a group.
func TestGroupLifecycle(t *testing.T) {
	const name = "itest-group"
	const renamed = "itest-group2"
	const member = "itest-member"
	removeGroupInVM(name)
	removeGroupInVM(renamed)
	t.Cleanup(func() { removeGroupInVM(name); removeGroupInVM(renamed) })
	createUser(t, member)

	assertTasks(t, apiStatus(t, http.StatusCreated, "POST", "/api/groups", map[string]string{"groupname": name}))
	if _, ok := groupByName(t, name); !ok {
		t.Fatalf("group %s not listed after create", name)
	}

	apiStatus(t, http.StatusBadRequest, "PUT", "/api/groups/"+name, map[string]string{"members": "itest-nobody-here"})

	assertTasks(t, apiOK(t, "PUT", "/api/groups/"+name, map[string]string{"members": member, "new_name": renamed}))
	g, ok := groupByName(t, renamed)
	if !ok || !slices.Contains(g.Members, member) {
		t.Fatalf("group after modify: %+v (found %v), want renamed with member %s", g, ok, member)
	}
	if _, ok := groupByName(t, name); ok {
		t.Fatalf("old group name %s still present after rename", name)
	}

	assertTasks(t, apiOK(t, "DELETE", "/api/groups/"+renamed, nil))
	if _, ok := groupByName(t, renamed); ok {
		t.Fatalf("group still present after delete")
	}

	apiStatus(t, http.StatusBadRequest, "POST", "/api/groups", map[string]string{"groupname": "bad name"})
	apiStatus(t, http.StatusForbidden, "DELETE", "/api/groups/daemon", nil)
	apiStatus(t, http.StatusNotFound, "DELETE", "/api/groups/"+renamed, nil)
}
