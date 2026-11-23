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
	for _, rt := range decode[[]replTask](t, apiOK(t, "GET", "/api/v1/replication/tasks", nil)) {
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
	for _, rt := range decode[[]replTask](t, apiOK(t, "GET", "/api/v1/replication/tasks", nil)) {
		if strings.HasPrefix(rt.Name, "itest") {
			_, _ = api(t, "DELETE", v1("/replication/tasks/%s", rt.ID), nil)
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

	apiStatus(t, http.StatusBadRequest, "POST", "/api/v1/replication/tasks", map[string]any{
		"name": "itest-bad", "source": src, "target": dst, "schedule": "0 3 * *",
	})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/v1/replication/tasks", map[string]any{
		"name": "itest-bad", "source": src, "target": "notarget", "schedule": "0 3 * * *",
	})

	created := decode[replTask](t, apiStatus(t, http.StatusCreated, "POST", "/api/v1/replication/tasks", map[string]any{
		"name": "itest-repl", "source": src, "target": dst, "schedule": "0 3 * * *",
	}))
	if created.ID == "" || !created.Enabled || created.RetentionCount != 7 {
		t.Fatalf("created task: %+v, want id set, enabled, retention 7", created)
	}
	if _, ok := replTaskByID(t, created.ID); !ok {
		t.Fatalf("task %s not listed after create", created.ID)
	}

	updated := decode[replTask](t, apiOK(t, "PATCH", v1("/replication/tasks/%s", created.ID), map[string]any{
		"retention_count": 3, "enabled": false,
	}))
	if updated.RetentionCount != 3 || updated.Enabled {
		t.Fatalf("task after PATCH: %+v, want retention 3, disabled", updated)
	}
	apiStatus(t, http.StatusNotFound, "PATCH", "/api/v1/replication/tasks/0000000000000000", map[string]any{})

	// A manual run works on a disabled task.
	run := decode[struct {
		JobID    string `json:"job_id"`
		Snapshot string `json:"snapshot"`
	}](t, apiStatus(t, http.StatusAccepted, "POST", v1("/replication/tasks/%s/run", created.ID), nil))
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
		hist = decode[[]runRecord](t, apiOK(t, "GET", v1("/replication/tasks/%s/history", created.ID), nil))
		return len(hist) > 0
	})
	if hist[len(hist)-1].JobID != run.JobID || hist[len(hist)-1].Status != "success" {
		t.Fatalf("history: %+v, want success record for job %s", hist, run.JobID)
	}
	// The watcher also releases the hold, so the source snapshot is destroyable.
	if out := vmExec(t, "zfs holds -H "+run.Snapshot); strings.TrimSpace(out) != "" {
		t.Fatalf("hold still present on %s after run: %s", run.Snapshot, out)
	}

	// A single DELETE releases a dumpstore-repl hold first, like the batch
	// path (#197); without that, zfs destroy fails with "dataset is busy".
	vmExec(t, "zfs hold dumpstore-repl "+run.Snapshot)
	assertTasks(t, apiOK(t, "DELETE", v1("/snapshots/%s", run.Snapshot), nil))
	for _, s := range listSnapshots(t) {
		if s.Name == run.Snapshot {
			t.Fatalf("held snapshot %s still present after single DELETE", run.Snapshot)
		}
	}

	apiStatus(t, http.StatusNoContent, "DELETE", v1("/replication/tasks/%s", created.ID), nil)
	if _, ok := replTaskByID(t, created.ID); ok {
		t.Fatalf("task %s still listed after delete", created.ID)
	}
	apiStatus(t, http.StatusNotFound, "DELETE", v1("/replication/tasks/%s", created.ID), nil)
}

// TestReplicationSourcePruning covers #221: every run leaves a snapshot on
// the source, so the source is pruned to retention_count after each
// successful run, like the target. Only the task's own snapshots go
// (labels start with dumpstore-repl-<task ID>-); an old-format snapshot and a manual one on the
// same source survive, and later runs stay incremental.
func TestReplicationSourcePruning(t *testing.T) {
	src := testPool + "/itest-prune-src"
	dst := testPool + "/itest-prune-dst"
	destroyDatasetInVM(dst)
	t.Cleanup(func() { deleteITestReplTasks(t); destroyDatasetInVM(dst) })
	deleteITestReplTasks(t)
	createDataset(t, src)
	legacy := src + "@dumpstore-repl-20200101T000000Z"
	manual := src + "@itest-manual"
	vmExec(t, "zfs snapshot "+legacy+" && zfs snapshot "+manual)

	task := decode[replTask](t, apiStatus(t, http.StatusCreated, "POST", "/api/v1/replication/tasks", map[string]any{
		"name": "itest-prune", "source": src, "target": dst, "schedule": "0 3 * * *", "retention_count": 2,
	}))
	type runRecord struct {
		JobID string `json:"job_id"`
	}
	var last string
	for i := 1; i <= 3; i++ {
		run := decode[struct {
			JobID    string `json:"job_id"`
			Snapshot string `json:"snapshot"`
		}](t, apiStatus(t, http.StatusAccepted, "POST", v1("/replication/tasks/%s/run", task.ID), nil))
		if !strings.HasPrefix(run.Snapshot, src+"@dumpstore-repl-"+task.ID+"-") {
			t.Fatalf("run %d snapshot %s does not carry the task ID", i, run.Snapshot)
		}
		j := waitJob(t, run.JobID, 2*time.Minute)
		if j.Status != "success" {
			t.Fatalf("run %d: job %s (%s)", i, j.Status, j.Stderr)
		}
		if incr := strings.Contains(" "+strings.Join(j.Args, " ")+" ", " -i "); incr != (i > 1) {
			t.Errorf("run %d: incremental=%v, want %v (args %v)", i, incr, i > 1, j.Args)
		}
		// Pruning happens in the post-job watcher, before the run record.
		waitFor(t, "run record", 30*time.Second, func() bool {
			hist := decode[[]runRecord](t, apiOK(t, "GET", v1("/replication/tasks/%s/history", task.ID), nil))
			return len(hist) > 0 && hist[len(hist)-1].JobID == run.JobID
		})
		last = run.Snapshot
		// Wait so the next run gets a new second-resolution label.
		time.Sleep(1100 * time.Millisecond)
	}

	names := func(dataset string) []string {
		out := strings.Fields(vmExec(t, "zfs list -H -t snapshot -o name -s creation -d 1 "+dataset))
		return out
	}
	var own []string
	srcSnaps := names(src)
	for _, s := range srcSnaps {
		if strings.HasPrefix(s, src+"@dumpstore-repl-"+task.ID+"-") {
			own = append(own, s)
		}
	}
	if len(own) != 2 || own[1] != last {
		t.Errorf("source keeps %v, want 2 of the task's snapshots ending with %s", own, last)
	}
	for _, keep := range []string{legacy, manual} {
		if !strings.Contains(" "+strings.Join(srcSnaps, " ")+" ", " "+keep+" ") {
			t.Errorf("%s was pruned; only the task's own snapshots may be", keep)
		}
	}
	if got := names(dst); len(got) != 2 {
		t.Errorf("target keeps %v, want 2", got)
	}
	vmExec(t, "zfs destroy "+legacy+" && zfs destroy "+manual)
}
