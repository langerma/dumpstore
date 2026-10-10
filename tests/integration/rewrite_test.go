//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestRewrite runs `zfs rewrite` on a small dataset as a background job.
// Skipped when the VM's OpenZFS predates the rewrite subcommand.
func TestRewrite(t *testing.T) {
	caps := decode[struct {
		Capabilities map[string]bool `json:"capabilities"`
	}](t, apiOK(t, "GET", "/api/schema", nil)).Capabilities
	if !caps["rewrite"] {
		t.Skip("zfs rewrite not supported by this OpenZFS version")
	}

	ds := testPool + "/itest-rewrite"
	mp := createDataset(t, ds)
	vmExec(t, fmt.Sprintf("dd if=/dev/urandom of=%s/blob bs=1M count=4 2>/dev/null", mp))

	res := decode[struct {
		JobID string `json:"job_id"`
		Type  string `json:"type"`
	}](t, apiStatus(t, http.StatusAccepted, "POST", "/api/rewrite/"+ds, map[string]any{}))
	if res.Type != "dataset.rewrite" {
		t.Errorf("job type %q, want dataset.rewrite", res.Type)
	}
	if j := waitJob(t, res.JobID, 2*time.Minute); j.Status != "success" {
		t.Fatalf("rewrite job ended %q (error %q, stderr %q)", j.Status, j.Error, j.Stderr)
	}

	vol := testPool + "/itest-rewrite-vol"
	destroyDatasetInVM(vol)
	t.Cleanup(func() { destroyDatasetInVM(vol) })
	apiOK(t, "POST", "/api/datasets", map[string]any{"name": vol, "type": "volume", "volsize": "16M"})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/rewrite/"+vol, map[string]any{})
	apiStatus(t, http.StatusNotFound, "POST", "/api/rewrite/"+testPool+"/itest-nonexistent", map[string]any{})
}
