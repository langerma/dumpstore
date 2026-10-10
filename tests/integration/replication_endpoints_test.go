//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

type replTask struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Source         string `json:"source"`
	Target         string `json:"target"`
	Schedule       string `json:"schedule"`
	RetentionCount int    `json:"retention_count"`
	Enabled        bool   `json:"enabled"`
}

func replTaskByID(t *testing.T, id string) (replTask, bool) {
	t.Helper()
	for _, rt := range decode[[]replTask](t, apiOK(t, "GET", "/api/replication", nil)) {
		if rt.ID == id {
			return rt, true
		}
	}
	return replTask{}, false
}

// deleteITestReplTasks removes replication tasks left behind by an aborted
// run — they persist in replication.json across restarts.
func deleteITestReplTasks(t *testing.T) {
	t.Helper()
	for _, rt := range decode[[]replTask](t, apiOK(t, "GET", "/api/replication", nil)) {
		if strings.HasPrefix(rt.Name, "itest") {
			_, _ = api(t, "DELETE", "/api/replication/"+rt.ID, nil)
		}
	}
}

// TestReplicationTask covers replication task CRUD, a manual run through the
// jobs manager, the received snapshot on the target, and the run history.
func TestReplicationTask(t *testing.T) {
	src := testPool + "/itest-repl-src"
	dst := testPool + "/itest-repl-dst"
	destroyDatasetInVM(dst)
	t.Cleanup(func() { deleteITestReplTasks(t); destroyDatasetInVM(dst) })
	deleteITestReplTasks(t)
	createDataset(t, src)

	apiStatus(t, http.StatusBadRequest, "POST", "/api/replication", map[string]any{
		"name": "itest-bad", "source": src, "target": dst, "schedule": "0 3 * *",
	})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/replication", map[string]any{
		"name": "itest-bad", "source": src, "target": "notarget", "schedule": "0 3 * * *",
	})

	created := decode[replTask](t, apiStatus(t, http.StatusCreated, "POST", "/api/replication", map[string]any{
		"name": "itest-repl", "source": src, "target": dst, "schedule": "0 3 * * *",
	}))
	if created.ID == "" || !created.Enabled || created.RetentionCount != 7 {
		t.Fatalf("created task: %+v, want id set, enabled, retention 7", created)
	}
	if _, ok := replTaskByID(t, created.ID); !ok {
		t.Fatalf("task %s not listed after create", created.ID)
	}

	updated := decode[replTask](t, apiOK(t, "PATCH", "/api/replication/"+created.ID, map[string]any{
		"retention_count": 3, "enabled": false,
	}))
	if updated.RetentionCount != 3 || updated.Enabled {
		t.Fatalf("task after PATCH: %+v, want retention 3, disabled", updated)
	}
	apiStatus(t, http.StatusNotFound, "PATCH", "/api/replication/0000000000000000", map[string]any{})

	// A manual run works on a disabled task.
	run := decode[struct {
		JobID    string `json:"job_id"`
		Snapshot string `json:"snapshot"`
	}](t, apiStatus(t, http.StatusAccepted, "POST", "/api/replication/"+created.ID+"/run", nil))
	if j := waitJob(t, run.JobID, 2*time.Minute); j.Status != "success" {
		t.Fatalf("replication job ended %q (error %q, stderr %q)", j.Status, j.Error, j.Stderr)
	}
	label := run.Snapshot[strings.Index(run.Snapshot, "@"):]
	var received bool
	for _, s := range listSnapshots(t) {
		received = received || s.Name == dst+label
	}
	if !received {
		t.Fatalf("target snapshot %s%s not present after run", dst, label)
	}

	// History is written by the post-job watcher, shortly after the job ends.
	type runRecord struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	var hist []runRecord
	waitFor(t, "run history record", 30*time.Second, func() bool {
		hist = decode[[]runRecord](t, apiOK(t, "GET", "/api/replication/"+created.ID+"/history", nil))
		return len(hist) > 0
	})
	if hist[len(hist)-1].JobID != run.JobID || hist[len(hist)-1].Status != "success" {
		t.Fatalf("history: %+v, want success record for job %s", hist, run.JobID)
	}
	// The watcher also releases the hold, so the source snapshot is destroyable.
	if out := vmExec(t, "zfs holds -H "+run.Snapshot); strings.TrimSpace(out) != "" {
		t.Fatalf("hold still present on %s after run: %s", run.Snapshot, out)
	}

	apiStatus(t, http.StatusNoContent, "DELETE", "/api/replication/"+created.ID, nil)
	if _, ok := replTaskByID(t, created.ID); ok {
		t.Fatalf("task %s still listed after delete", created.ID)
	}
	apiStatus(t, http.StatusNotFound, "DELETE", "/api/replication/"+created.ID, nil)
}
