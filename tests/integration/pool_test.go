//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const itestPool = "itestpool"

// base returns the short device name zpool status displays ("/dev/vdd" -> "vdd").
func base(dev string) string {
	return dev[strings.LastIndexByte(dev, '/')+1:]
}

func hasVdev(d poolDetail, name string) bool {
	for _, v := range d.Vdevs {
		if v.Name == name {
			return true
		}
	}
	return false
}

func vdevState(d poolDetail, name string) string {
	for _, v := range d.Vdevs {
		if v.Name == name {
			return v.State
		}
	}
	return ""
}

func cleanupItestPool() {
	_, _ = vmExecErr("zpool destroy -f " + itestPool + " 2>/dev/null || true")
	for _, d := range testDisks {
		clearDiskLabels(d)
	}
}

// clearDiskLabels removes any stale ZFS labels and partition tables so the
// disk can be reused. ZFS labels whole-disk vdevs on partition 1, so both
// locations are cleared. Best-effort: tools or labels may be absent.
func clearDiskLabels(dev string) {
	_, _ = vmExecErr("zpool labelclear -f " + dev + "1 2>/dev/null; " +
		"zpool labelclear -f " + dev + " 2>/dev/null; " +
		"wipefs -a " + dev + " 2>/dev/null; true")
}

// TestPoolLifecycle walks a scratch pool through its whole life on the VM's
// dedicated test disks: create (mirror), device offline/online, disk
// replacement with resilver, spare add/remove, export, import, destroy.
func TestPoolLifecycle(t *testing.T) {
	if len(testDisks) < 3 {
		t.Skipf("need 3 test disks, have %v (set DUMPSTORE_TEST_DISKS)", testDisks)
	}
	d0, d1, d2 := testDisks[0], testDisks[1], testDisks[2]

	cleanupItestPool()
	t.Cleanup(cleanupItestPool)

	// Create a two-way mirror.
	apiOK(t, "POST", "/api/pools", map[string]any{
		"name": itestPool, "vdev_type": "mirror", "devices": []string{d0, d1},
	})
	if p, ok := poolByName(t, itestPool); !ok || p.Health != "ONLINE" {
		t.Fatalf("pool after create: present=%v health=%q, want ONLINE", ok, p.Health)
	}

	// Creating a pool with a name that already exists must be refused.
	if status, _ := api(t, "POST", "/api/pools", map[string]any{
		"name": itestPool, "vdev_type": "single", "devices": []string{d2},
	}); status != http.StatusConflict {
		t.Fatalf("duplicate pool create: got %d, want 409", status)
	}

	// Offline one side of the mirror, then bring it back.
	apiOK(t, "POST", "/api/pools/"+itestPool+"/offline", map[string]string{"device": d1})
	waitFor(t, "vdev offline", 30*time.Second, func() bool {
		d, ok := poolStatus(t, itestPool)
		return ok && vdevState(d, base(d1)) == "OFFLINE"
	})
	apiOK(t, "POST", "/api/pools/"+itestPool+"/online", map[string]string{"device": d1})
	waitFor(t, "pool back to ONLINE", time.Minute, func() bool {
		d, ok := poolStatus(t, itestPool)
		return ok && d.State == "ONLINE" && vdevState(d, base(d1)) == "ONLINE"
	})

	// Replace the second mirror leg with the third disk and let it resilver.
	apiOK(t, "POST", "/api/pools/"+itestPool+"/replace", map[string]string{
		"old_device": d1, "new_device": d2,
	})
	waitFor(t, "resilver onto "+d2, 2*time.Minute, func() bool {
		d, ok := poolStatus(t, itestPool)
		return ok && d.State == "ONLINE" && hasVdev(d, base(d2)) && !hasVdev(d, base(d1))
	})

	// The freed disk becomes a hot spare, then is removed again. The replace
	// detached it with its old label intact, so clear it first.
	clearDiskLabels(d1)
	apiOK(t, "POST", "/api/pools/"+itestPool+"/spare", map[string]any{
		"devices": []string{d1},
	})
	waitFor(t, "spare visible", 30*time.Second, func() bool {
		d, ok := poolStatus(t, itestPool)
		return ok && hasVdev(d, base(d1))
	})
	apiOK(t, "DELETE", "/api/pools/"+itestPool+"/devices/"+base(d1), nil)
	waitFor(t, "spare removed", 30*time.Second, func() bool {
		d, ok := poolStatus(t, itestPool)
		return ok && !hasVdev(d, base(d1))
	})

	// Export: pool leaves the active list and shows up as importable.
	apiOK(t, "POST", "/api/pools/"+itestPool+"/export", nil)
	if _, ok := poolByName(t, itestPool); ok {
		t.Fatalf("pool %s still active after export", itestPool)
	}
	importable := decode[[]struct {
		Name string `json:"name"`
	}](t, apiOK(t, "GET", "/api/pools/importable", nil))
	found := false
	for _, p := range importable {
		if p.Name == itestPool {
			found = true
		}
	}
	if !found {
		t.Fatalf("pool %s not in importable list after export: %+v", itestPool, importable)
	}

	// Import it back.
	apiOK(t, "POST", "/api/pools/import", map[string]any{"pool": itestPool})
	if p, ok := poolByName(t, itestPool); !ok || p.Health != "ONLINE" {
		t.Fatalf("pool after import: present=%v health=%q, want ONLINE", ok, p.Health)
	}

	// Scrub: on a tiny pool it may finish almost instantly, so assert the
	// scan status mentions a scrub at all (running or completed).
	apiOK(t, "POST", "/api/scrub/"+itestPool, nil)
	waitFor(t, "scrub to appear in scan status", 30*time.Second, func() bool {
		d, ok := poolStatus(t, itestPool)
		return ok && strings.Contains(d.Scan, "scrub")
	})
}
