package replication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"dumpstore/internal/jobs"
	"dumpstore/internal/scheduler"
)

// Publisher is the subset of broker.Broker the runner uses to push UI updates.
// Decoupling keeps the package independent of the broker concrete type and
// makes unit testing trivial.
type Publisher interface {
	Publish(topic string, data any)
}

// JobsRunner is the subset of jobs.Manager the runner depends on. Mirrors the
// Publisher pattern so tests can swap in a fake.
type JobsRunner interface {
	RunPipeline(jobType string, left, right []string) (jobs.Job, error)
	Run(jobType string, argv []string) (jobs.Job, error)
	Get(id string) (jobs.Job, bool)
}

// Runner glues Store + Scheduler + JobsRunner together. It owns the policy
// for what a scheduled replication run actually does: snapshot, hold, send,
// release, prune.
type Runner struct {
	store *Store
	sched Scheduler
	jobs  JobsRunner
	pub   Publisher
	poll  time.Duration // job-completion poll interval
}

// Scheduler is the cron-firing engine. Defined as an interface here so a test
// can drive scheduling synchronously without spinning up the real loop.
type Scheduler interface {
	Register(id string, schedule scheduler.Schedule, fn scheduler.TaskFunc)
	Unregister(id string)
}

// NewRunner constructs a Runner. pub may be nil (no SSE updates).
func NewRunner(store *Store, sched Scheduler, j JobsRunner, pub Publisher) *Runner {
	return &Runner{
		store: store,
		sched: sched,
		jobs:  j,
		pub:   pub,
		poll:  2 * time.Second,
	}
}

// Store returns the underlying task store. Handlers use it for read-only
// access (List, Get, history) and for direct Create/Update/Delete; the runner
// itself only mutates the store via AppendRun.
func (r *Runner) Store() *Store { return r.store }

// LoadAndRegisterAll registers every Enabled task with the scheduler and
// releases any orphaned `dumpstore-repl` holds left behind by runs that were
// interrupted (service crash, OS reboot, kill -9) between `zfs hold` and the
// matching `zfs release` in afterJob. Call once at startup after the Store
// is loaded — at this point no runs are in flight, so every dumpstore-repl
// hold is by definition stale.
func (r *Runner) LoadAndRegisterAll() error {
	r.releaseOrphanedHolds()
	for _, t := range r.store.List() {
		if !t.Enabled {
			continue
		}
		if err := r.register(t); err != nil {
			slog.Error("replication: register failed at startup", "task", t.ID, "err", err)
		}
	}
	return nil
}

// releaseOrphanedHolds scans every dumpstore-repl-* snapshot on the host and
// drops the dumpstore-repl hold tag if present. Without this, a snapshot left
// held by a crashed run cannot be destroyed by the user from the UI
// (`zfs destroy` reports "dataset is busy") and accumulates indefinitely.
func (r *Runner) releaseOrphanedHolds() {
	out, err := runOut("zfs", "list", "-H", "-t", "snapshot", "-o", "name")
	if err != nil {
		slog.Warn("replication: list snapshots for hold cleanup failed", "err", err)
		return
	}
	released := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || !strings.Contains(line, "@"+SnapshotPrefix) {
			continue
		}
		// `-r` cleans holds on child snapshots too (recursive replication
		// tasks placed holds recursively). releaseSnapshot already swallows
		// "no such tag" — non-recursive snapshots stay untouched.
		if err := releaseSnapshot(HoldTag, line, true); err == nil {
			released++
		}
	}
	if released > 0 {
		slog.Info("replication: released orphaned holds from prior runs", "count", released)
	}
}

// RegisterEnabled (re)registers a task with the scheduler if Enabled is set,
// or unregisters it if not. Idempotent.
func (r *Runner) RegisterEnabled(t Task) error {
	if !t.Enabled {
		r.sched.Unregister(t.ID)
		return nil
	}
	return r.register(t)
}

// Unregister removes a task from the scheduler (called from the delete handler).
func (r *Runner) Unregister(id string) {
	r.sched.Unregister(id)
}

// ReleaseHoldsFor drops the dumpstore-repl hold from any of the given full
// snapshot names that look like one of ours (`*@dumpstore-repl-*`). Called
// from the snapshot batch-destroy handler so that user-initiated deletes of
// our held snapshots succeed instead of failing with "dataset is busy".
//
// Non-dumpstore snapshots are skipped; snapshots without our hold are a
// silent no-op (releaseSnapshot swallows "no such tag").
func (r *Runner) ReleaseHoldsFor(snapshots []string) {
	for _, snap := range snapshots {
		i := strings.IndexByte(snap, '@')
		if i < 0 {
			continue
		}
		if !strings.HasPrefix(snap[i+1:], SnapshotPrefix) {
			continue
		}
		_ = releaseSnapshot(HoldTag, snap, true)
	}
}

// RunOnce fires the task immediately, off-schedule, in a new goroutine. It
// returns the snapshot name the run will create and the kicking-off job_id
// once the send/recv has been dispatched — or an error if a hard precondition
// fails (task missing, snapshot creation failed, etc.).
//
// Because the actual send may take hours, RunOnce does not wait for it. The
// caller subscribes to the jobs topic for completion.
func (r *Runner) RunOnce(ctx context.Context, id string) (snap string, jobID string, err error) {
	t, err := r.store.Get(id)
	if err != nil {
		return "", "", err
	}
	return r.execute(ctx, t)
}

func (r *Runner) register(t Task) error {
	sch, err := scheduler.Parse(t.Schedule)
	if err != nil {
		return fmt.Errorf("parse schedule %q: %w", t.Schedule, err)
	}
	id := t.ID
	r.sched.Register(id, sch, func(ctx context.Context) {
		// Re-load the task to pick up any updates since registration.
		current, err := r.store.Get(id)
		if err != nil {
			slog.Warn("replication: task vanished before run", "task", id)
			return
		}
		if !current.Enabled {
			return
		}
		if _, _, err := r.execute(ctx, current); err != nil {
			slog.Error("replication: run failed", "task", id, "err", err)
		}
	})
	return nil
}

// execute performs a single replication run end-to-end. Returns the source
// snapshot name and the dispatched job_id once the send/recv is in flight,
// then spawns a goroutine to wait for completion and record the outcome.
func (r *Runner) execute(ctx context.Context, t Task) (string, string, error) {
	now := time.Now().UTC()
	snap := snapName(now)
	srcSnap := t.Source + "@" + snap

	if err := createSnapshot(t.Source, snap, t.Recursive); err != nil {
		return "", "", fmt.Errorf("create snapshot: %w", err)
	}
	if err := holdSnapshot(HoldTag, srcSnap, t.Recursive); err != nil {
		// Best effort: try to clean up the snapshot we just made so we don't
		// leave a tombstone on the source.
		_ = destroyRemoteOrLocal(srcSnap, "")
		return "", "", fmt.Errorf("hold snapshot: %w", err)
	}

	prev, err := findLastCommon(t.Source, t.Target, t.Remote)
	if err != nil {
		_ = releaseSnapshot(HoldTag, srcSnap, t.Recursive)
		return "", "", fmt.Errorf("find common snapshot: %w", err)
	}

	send := []string{"zfs", "send"}
	if t.Raw {
		send = append(send, "--raw")
	}
	if t.Recursive {
		send = append(send, "-R")
	}
	if prev != "" {
		send = append(send, "-i", prev)
	}
	send = append(send, srcSnap)

	// `-F` rolls the destination forward over any incidental local changes and
	// is required for `-R` property updates to apply cleanly. `-u` skips
	// mounting on receive — the destination is meant to be a passive mirror,
	// not a live filesystem on the backup host. Replication tools (zettarepl,
	// TrueNAS) use these flags unconditionally; we match.
	recvArgs := []string{"zfs", "recv", "-F", "-u", t.Target}
	var recv []string
	if t.Remote != "" {
		recv = append([]string{"ssh", "-o", "BatchMode=yes", t.Remote}, recvArgs...)
	} else {
		recv = recvArgs
	}

	job, err := r.jobs.RunPipeline("replication.run", send, recv)
	if err != nil {
		_ = releaseSnapshot(HoldTag, srcSnap, t.Recursive)
		return "", "", fmt.Errorf("dispatch job: %w", err)
	}

	// Wait for completion in a goroutine so the caller (scheduler tick or
	// the manual-run handler) returns immediately. The wait is bounded only
	// by the job itself; cancel propagates via the jobs manager.
	go r.afterJob(ctx, t.ID, srcSnap, t, job.ID, now)

	return srcSnap, job.ID, nil
}

func (r *Runner) afterJob(ctx context.Context, taskID, srcSnap string, t Task, jobID string, startedAt time.Time) {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		j, ok := r.jobs.Get(jobID)
		if !ok {
			r.finishRun(t, srcSnap, jobID, startedAt, "failed", errors.New("job record disappeared"))
			return
		}
		if !isTerminal(j.Status) {
			continue
		}
		// Release the hold either way. Pruning only on success.
		_ = releaseSnapshot(HoldTag, srcSnap, t.Recursive)
		var runErr error
		if j.Status != jobs.StatusSuccess {
			runErr = errors.New(string(j.Status))
			if j.Error != "" {
				runErr = errors.New(j.Error)
			}
		}
		if j.Status == jobs.StatusSuccess {
			if destroyed, err := pruneTarget(t.Target, t.Remote, t.RetentionCount); err != nil {
				slog.Warn("replication: prune failed", "task", t.ID, "err", err)
			} else if len(destroyed) > 0 {
				slog.Info("replication: pruned destination snapshots",
					"task", t.ID, "count", len(destroyed))
			}
		}
		r.finishRun(t, srcSnap, jobID, startedAt, string(j.Status), runErr)
		return
	}
}

func (r *Runner) finishRun(t Task, srcSnap, jobID string, startedAt time.Time, status string, runErr error) {
	rec := RunRecord{
		JobID:      jobID,
		Snapshot:   srcSnap,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		Status:     status,
	}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	if err := r.store.AppendRun(t.ID, rec); err != nil {
		slog.Warn("replication: append run record failed", "task", t.ID, "err", err)
	}
	if r.pub != nil {
		r.pub.Publish("replication.update", r.store.List())
	}
}

func isTerminal(s jobs.Status) bool {
	switch s {
	case jobs.StatusSuccess, jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusInterrupted:
		return true
	}
	return false
}
