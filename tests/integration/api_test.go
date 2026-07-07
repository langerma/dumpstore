//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAuth verifies the session gate: unauthenticated API calls are rejected,
// bad credentials bounce back to the login page, good credentials set a session.
func TestAuth(t *testing.T) {
	// No cookie yet (login() has not run in this fresh client for this request):
	// use a bare request without the shared jar's session by hitting the API
	// through a throwaway client.
	resp, err := http.Get(baseURL + "/api/pools")
	if err != nil {
		t.Fatalf("GET /api/pools: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/pools: got %d, want 401", resp.StatusCode)
	}

	if resp := postLogin(t, adminUser, "definitely-wrong-password"); resp.StatusCode != http.StatusFound ||
		!strings.HasPrefix(resp.Header.Get("Location"), "/login?error=") {
		t.Fatalf("bad-password login: got %d -> %q, want 302 -> /login?error=...",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	login(t) // asserts 302 -> / with a session cookie
	body := apiOK(t, "GET", "/api/version", nil)
	v := decode[map[string]string](t, body)
	if v["version"] == "" {
		t.Fatalf("GET /api/version returned empty version: %s", body)
	}
	t.Logf("ZFS version on VM: %s", strings.ReplaceAll(v["version"], "\n", " "))
}

// TestDatasetLifecycle creates a filesystem through the API, updates a
// property, renames it, and destroys it — verifying real ZFS state after
// each mutation.
func TestDatasetLifecycle(t *testing.T) {
	name := testPool + "/itest-ds"
	renamed := testPool + "/itest-ds-renamed"
	destroyDatasetInVM(name)
	destroyDatasetInVM(renamed)
	t.Cleanup(func() { destroyDatasetInVM(name); destroyDatasetInVM(renamed) })

	apiOK(t, "POST", "/api/datasets", map[string]any{
		"name": name, "type": "filesystem", "compression": "lz4",
	})
	ds, ok := datasetByName(t, name)
	if !ok {
		t.Fatalf("dataset %s not in /api/datasets after create", name)
	}
	if ds.Compression != "lz4" {
		t.Errorf("compression after create: got %q, want lz4", ds.Compression)
	}

	apiOK(t, "PATCH", "/api/datasets/"+name, map[string]string{"compression": "zstd"})
	if ds, _ := datasetByName(t, name); ds.Compression != "zstd" {
		t.Errorf("compression after PATCH: got %q, want zstd", ds.Compression)
	}

	apiOK(t, "POST", "/api/datasets/rename", map[string]string{
		"name": name, "new_name": renamed,
	})
	if _, ok := datasetByName(t, renamed); !ok {
		t.Fatalf("dataset %s not found after rename", renamed)
	}
	if _, ok := datasetByName(t, name); ok {
		t.Fatalf("old name %s still present after rename", name)
	}

	apiOK(t, "DELETE", "/api/datasets/"+renamed, nil)
	if _, ok := datasetByName(t, renamed); ok {
		t.Fatalf("dataset %s still present after delete", renamed)
	}
}

// TestSnapshotsAndDiff exercises snapshot create, list, zfs diff between two
// snapshots, clone, and batch delete — with file changes made directly in the
// VM between snapshots.
func TestSnapshotsAndDiff(t *testing.T) {
	name := testPool + "/itest-snap"
	clone := testPool + "/itest-clone"
	destroyDatasetInVM(clone)
	destroyDatasetInVM(name)
	t.Cleanup(func() { destroyDatasetInVM(clone); destroyDatasetInVM(name) })

	apiOK(t, "POST", "/api/datasets", map[string]any{"name": name, "type": "filesystem"})
	ds, ok := datasetByName(t, name)
	if !ok || ds.Mountpoint == "" {
		t.Fatalf("dataset %s missing or has no mountpoint: %+v", name, ds)
	}

	vmExec(t, fmt.Sprintf("echo one > %s/first.txt", ds.Mountpoint))
	apiOK(t, "POST", "/api/snapshots", map[string]any{"dataset": name, "snapname": "s1"})

	vmExec(t, fmt.Sprintf("echo two > %s/second.txt", ds.Mountpoint))
	apiOK(t, "POST", "/api/snapshots", map[string]any{"dataset": name, "snapname": "s2"})

	var found int
	for _, s := range listSnapshots(t) {
		if s.Name == name+"@s1" || s.Name == name+"@s2" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected both snapshots in /api/snapshots, found %d", found)
	}

	diffBody := apiOK(t, "GET",
		"/api/snapshots/diff?from="+name+"@s1&to="+name+"@s2", nil)
	diff := decode[struct {
		Entries []struct {
			Change string `json:"change"`
			Path   string `json:"path"`
		} `json:"entries"`
	}](t, diffBody)
	var sawNewFile bool
	for _, e := range diff.Entries {
		if e.Change == "+" && strings.HasSuffix(e.Path, "/second.txt") {
			sawNewFile = true
		}
	}
	if !sawNewFile {
		t.Fatalf("diff s1..s2 did not report second.txt as created: %s", truncate(diffBody, 2000))
	}

	apiOK(t, "POST", "/api/snapshots/clone", map[string]string{
		"snapshot": name + "@s2", "target": clone,
	})
	if _, ok := datasetByName(t, clone); !ok {
		t.Fatalf("clone %s not in /api/datasets", clone)
	}
	// The clone depends on s2, so drop it before deleting the snapshots.
	apiOK(t, "DELETE", "/api/datasets/"+clone, nil)

	apiOK(t, "POST", "/api/snapshots/delete-batch", map[string]any{
		"snapshots": []string{name + "@s1", name + "@s2"},
	})
	for _, s := range listSnapshots(t) {
		if s.Dataset == name {
			t.Fatalf("snapshot %s survived batch delete", s.Name)
		}
	}
}

// TestUserQuota sets and clears a per-user quota and verifies it via the
// userspace report.
func TestUserQuota(t *testing.T) {
	name := testPool + "/itest-quota"
	destroyDatasetInVM(name)
	t.Cleanup(func() { destroyDatasetInVM(name) })

	apiOK(t, "POST", "/api/datasets", map[string]any{"name": name, "type": "filesystem"})

	apiOK(t, "POST", "/api/userquota/"+name, map[string]string{
		"kind": "user", "principal": "root", "quota": "25M",
	})
	rows := decode[struct {
		Rows []spaceRow `json:"rows"`
	}](t, apiOK(t, "GET", "/api/userspace/"+name+"?kind=user", nil))
	const want = 25 * 1024 * 1024
	var got uint64
	for _, r := range rows.Rows {
		if r.Name == "root" {
			got = r.Quota
		}
	}
	if got != want {
		t.Fatalf("root quota after set: got %d, want %d", got, want)
	}

	apiOK(t, "POST", "/api/userquota/"+name, map[string]string{
		"kind": "user", "principal": "root", "quota": "none",
	})
	rows = decode[struct {
		Rows []spaceRow `json:"rows"`
	}](t, apiOK(t, "GET", "/api/userspace/"+name+"?kind=user", nil))
	for _, r := range rows.Rows {
		if r.Name == "root" && r.Quota != 0 {
			t.Fatalf("root quota not cleared: still %d", r.Quota)
		}
	}
}

// TestSendReceiveJob runs a local zfs send | zfs recv through the jobs
// manager and follows the job to completion via the jobs API.
func TestSendReceiveJob(t *testing.T) {
	src := testPool + "/itest-send"
	dst := testPool + "/itest-recv"
	destroyDatasetInVM(src)
	destroyDatasetInVM(dst)
	t.Cleanup(func() { destroyDatasetInVM(src); destroyDatasetInVM(dst) })

	apiOK(t, "POST", "/api/datasets", map[string]any{"name": src, "type": "filesystem"})
	ds, _ := datasetByName(t, src)
	vmExec(t, fmt.Sprintf("dd if=/dev/urandom of=%s/blob bs=1M count=8 2>/dev/null", ds.Mountpoint))
	apiOK(t, "POST", "/api/snapshots", map[string]any{"dataset": src, "snapname": "xfer"})

	status, body := api(t, "POST", "/api/snapshots/send", map[string]any{
		"snapshot": src + "@xfer", "target": dst,
	})
	if status != http.StatusAccepted {
		t.Fatalf("POST /api/snapshots/send: got %d, want 202; body: %s", status, truncate(body, 2000))
	}
	jobID := decode[struct {
		JobID string `json:"job_id"`
	}](t, body).JobID
	if jobID == "" {
		t.Fatalf("no job_id in send response: %s", body)
	}

	var j job
	waitFor(t, "send job to finish", 2*time.Minute, func() bool {
		j = decode[job](t, apiOK(t, "GET", "/api/jobs/"+jobID, nil))
		return j.Status != "pending" && j.Status != "running"
	})
	if j.Status != "success" {
		t.Fatalf("send job ended %q (error %q, stderr %q)", j.Status, j.Error, j.Stderr)
	}
	if _, ok := datasetByName(t, dst); !ok {
		t.Fatalf("received dataset %s not in /api/datasets", dst)
	}

	if status, body := api(t, "DELETE", "/api/jobs/"+jobID, nil); status != http.StatusNoContent {
		t.Fatalf("DELETE job: got %d, body: %s", status, truncate(body, 500))
	}
}
