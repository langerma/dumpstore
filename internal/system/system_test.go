package system

import (
	"testing"
	"time"
)

// TestCachedProbes asserts the software/warning probes run once per probeTTL
// rather than on every sysinfo request (#139).
func TestCachedProbes(t *testing.T) {
	calls := 0
	orig := collectProbes
	collectProbes = func() ([]SoftwareTool, []string) {
		calls++
		return []SoftwareTool{{Name: "ZFS", Version: "zfs-2.2", Required: true}}, []string{"w"}
	}
	t.Cleanup(func() {
		collectProbes = orig
		probeCache.at = time.Time{}
	})
	probeCache.at = time.Time{}

	for range 3 {
		sw, w := cachedProbes()
		if len(sw) != 1 || sw[0].Name != "ZFS" || len(w) != 1 {
			t.Fatalf("unexpected probe results: %+v %v", sw, w)
		}
	}
	if calls != 1 {
		t.Fatalf("probes ran %d times within TTL, want 1", calls)
	}

	// Expire the cache: the next call must re-probe.
	probeCache.at = time.Now().Add(-probeTTL)
	cachedProbes()
	if calls != 2 {
		t.Fatalf("probes ran %d times after TTL expiry, want 2", calls)
	}
}
