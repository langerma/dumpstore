//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// freshLogin attempts a login on a new, cookie-less client and reports
// whether it succeeded — independent of the shared session.
func freshLogin(t *testing.T, user, pass string) bool {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(baseURL+"/auth/login", url.Values{"username": {user}, "password": {pass}})
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusFound && resp.Header.Get("Location") == "/"
}

// TestChangePassword changes the admin password, proves the existing session
// survives and only the new password logs in, then changes it back.
// change-username is deliberately not covered: it invalidates every session,
// and a failure mid-test would strand the VM with unknown credentials.
func TestChangePassword(t *testing.T) {
	const newPass = "itest-new-password"
	backupDumpstoreConf(t)
	change := func(cur, next string) (int, []byte) {
		return api(t, "POST", "/api/auth/change-password", map[string]string{
			"current_password": cur, "new_password": next,
		})
	}
	reverted := false
	t.Cleanup(func() {
		// Prefer the API so the running process's in-memory hash is restored
		// too; the conf backup covers the next start if this fails.
		if !reverted {
			if status, b := change(newPass, adminPass); status != http.StatusOK {
				t.Errorf("reverting admin password failed (%d): %s", status, truncate(b, 500))
			}
		}
	})

	apiStatus(t, http.StatusUnauthorized, "POST", "/api/auth/change-password", map[string]string{
		"current_password": "definitely-wrong", "new_password": newPass,
	})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/auth/change-password", map[string]string{
		"current_password": adminPass, "new_password": "",
	})

	status, b := change(adminPass, newPass)
	if status != http.StatusOK {
		reverted = true // nothing changed, nothing to revert
		t.Fatalf("change-password: status %d, body: %s", status, truncate(b, 2000))
	}
	assertTasks(t, b)
	if out := vmExec(t, "cat /etc/dumpstore/dumpstore.conf 2>/dev/null || cat /usr/local/etc/dumpstore/dumpstore.conf"); !strings.Contains(out, `"$argon2id$`) {
		t.Errorf("stored hash is not argon2id")
	}

	// The current session is not invalidated by a password change.
	apiOK(t, "GET", "/api/whoami", nil)
	if freshLogin(t, adminUser, adminPass) {
		t.Errorf("old password still logs in after change")
	}
	if !freshLogin(t, adminUser, newPass) {
		t.Errorf("new password does not log in")
	}

	if status, b := change(newPass, adminPass); status != http.StatusOK {
		t.Fatalf("change back: status %d, body: %s", status, truncate(b, 2000))
	}
	reverted = true
	if !freshLogin(t, adminUser, adminPass) {
		t.Fatalf("original password does not log in after revert")
	}
}
