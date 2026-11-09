//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestISCSITarget exports a zvol as an iSCSI target and removes it again,
// on targetcli/LIO (Linux) or ctld (FreeBSD) — see iscsiFixture.
func TestISCSITarget(t *testing.T) {
	zvol := testPool + "/itest-vol"
	const iqn = "iqn.2026-10.local.dumpstore:itest"

	destroyDatasetInVM(zvol)
	iscsiFixture(t, zvol, iqn)
	t.Cleanup(func() { destroyDatasetInVM(zvol) })

	apiOK(t, "POST", "/api/datasets", map[string]any{"name": zvol, "type": "volume", "volsize": "64M"})
	// udev creates /dev/zvol/<name> asynchronously after zfs create.
	waitFor(t, "zvol device node", 30*time.Second, func() bool {
		_, err := vmExecErr("test -e /dev/zvol/" + zvol)
		return err == nil
	})

	// CHAP without a password is refused before anything runs.
	apiStatus(t, http.StatusBadRequest, "POST", "/api/iscsi-targets", map[string]any{
		"zvol": zvol, "iqn": iqn, "auth_mode": "chap", "chap_user": "itest",
	})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/iscsi-targets", map[string]any{
		"zvol": zvol, "iqn": "not-an-iqn", "auth_mode": "none",
	})

	assertTasks(t, apiOK(t, "POST", "/api/iscsi-targets", map[string]any{
		"zvol": zvol, "iqn": iqn, "auth_mode": "none",
	}))
	if !hasTarget(t, iqn, zvol) {
		t.Fatalf("target %s not listed after create", iqn)
	}

	q := "?iqn=" + url.QueryEscape(iqn) + "&zvol=" + url.QueryEscape(zvol)
	assertTasks(t, apiOK(t, "DELETE", "/api/iscsi-targets"+q, nil))
	if hasTarget(t, iqn, zvol) {
		t.Fatalf("target %s still listed after delete", iqn)
	}
	// Deleting the target leaves the zvol itself alone.
	if _, ok := datasetByName(t, zvol); !ok {
		t.Fatalf("zvol %s gone after target delete", zvol)
	}
}

func hasTarget(t *testing.T, iqn, zvol string) bool {
	t.Helper()
	// null when targets exist but none are zvol-backed.
	for _, tg := range decode[[]struct {
		IQN      string `json:"iqn"`
		ZvolName string `json:"zvol_name"`
	}](t, apiOK(t, "GET", "/api/iscsi-targets", nil)) {
		if tg.IQN == iqn && tg.ZvolName == zvol {
			return true
		}
	}
	return false
}
