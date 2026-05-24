package autosnap

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"dumpstore/internal/scheduler"
	"dumpstore/internal/zfs"
)

// Publisher is the subset of broker.Broker used to push status updates.
type Publisher interface {
	Publish(topic string, data any)
}

// schedulerIface is the local view of scheduler.Scheduler. Lets tests inject
// a fake.
type schedulerIface interface {
	Register(id string, schedule scheduler.Schedule, fn scheduler.TaskFunc)
	Unregister(id string)
}

// Runner owns the five cron tasks and the per-bucket processing logic. It is
// safe for concurrent use; Register/Unregister are guarded by a mutex.
type Runner struct {
	sched schedulerIface
	pub   Publisher

	mu         sync.Mutex
	registered bool
	lastRun    map[Bucket]time.Time
}

// New constructs a Runner. pub may be nil.
func New(sched schedulerIface, pub Publisher) *Runner {
	return &Runner{sched: sched, pub: pub, lastRun: make(map[Bucket]time.Time)}
}

// taskID is the scheduler key for a bucket. Stable so re-registration is idempotent.
func taskID(b Bucket) string { return "autosnap." + string(b) }

// Register attaches the five bucket tasks to the scheduler. Idempotent.
// Returns nil even if already registered.
func (r *Runner) Register() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range AllBuckets {
		sch, err := scheduler.Parse(b.CronExpression())
		if err != nil {
			return fmt.Errorf("parse %s: %w", b, err)
		}
		bucket := b
		r.sched.Register(taskID(bucket), sch, func(ctx context.Context) {
			r.processBucket(ctx, bucket)
		})
	}
	r.registered = true
	return nil
}

// Unregister removes all five tasks from the scheduler. Idempotent.
func (r *Runner) Unregister() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range AllBuckets {
		r.sched.Unregister(taskID(b))
	}
	r.registered = false
}

// IsRegistered reports whether the scheduler is currently running bucket tasks.
func (r *Runner) IsRegistered() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registered
}

// LastRuns returns a copy of the per-bucket last-run timestamps, for the
// status endpoint.
func (r *Runner) LastRuns() map[Bucket]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[Bucket]time.Time, len(r.lastRun))
	for k, v := range r.lastRun {
		out[k] = v
	}
	return out
}

// processBucket runs one tick of one bucket: for every dataset with that
// bucket enabled, create a new snapshot and prune the oldest beyond keep.
//
// Iteration is sequential per dataset — these are sub-second operations and
// avoiding goroutine fan-out keeps logs / failure handling simple. Errors on
// one dataset don't stop the rest.
func (r *Runner) processBucket(ctx context.Context, b Bucket) {
	props, err := zfs.ListAutoSnapshotProps()
	if err != nil {
		slog.Error("autosnap: list props failed", "bucket", b, "err", err)
		return
	}
	now := time.Now()
	label := b.SnapshotLabel(now)

	created, pruned := 0, 0
	for dataset, p := range props {
		enabled, keep := evalBucket(p, b)
		if !enabled {
			continue
		}
		snap := dataset + "@" + label
		if err := runCmd("zfs", "snapshot", snap); err != nil {
			slog.Error("autosnap: snapshot failed", "dataset", dataset, "bucket", b, "err", err)
			continue
		}
		created++
		if destroyed, err := pruneBucket(dataset, b, keep); err != nil {
			slog.Warn("autosnap: prune failed", "dataset", dataset, "bucket", b, "err", err)
		} else {
			pruned += destroyed
		}
	}
	r.mu.Lock()
	r.lastRun[b] = now.UTC()
	r.mu.Unlock()
	if created > 0 || pruned > 0 {
		slog.Info("autosnap: tick complete",
			"bucket", b, "created", created, "pruned", pruned)
		if r.pub != nil {
			r.pub.Publish("snapshot.query", nil) // best-effort trigger UI refresh
		}
	}
}

// evalBucket is a pure helper that returns (enabled, keepN) for the given
// bucket on a dataset whose effective properties are p. Pulled out for unit
// testing — covers the master=true gate, the per-bucket positive-int parse,
// and the all-false/missing/zero rejections.
func evalBucket(p zfs.AutoSnapshotProps, b Bucket) (bool, int) {
	if p.Master.Value != "true" {
		return false, 0
	}
	var v string
	switch b {
	case BucketFrequent:
		v = p.Frequent.Value
	case BucketHourly:
		v = p.Hourly.Value
	case BucketDaily:
		v = p.Daily.Value
	case BucketWeekly:
		v = p.Weekly.Value
	case BucketMonthly:
		v = p.Monthly.Value
	}
	keep, ok := keepFromValue(v)
	if !ok {
		return false, 0
	}
	return true, keep
}

// pruneBucket lists the dataset's snapshots in the bucket (by prefix match,
// sorted oldest first) and destroys any beyond keep. Returns the number
// destroyed.
func pruneBucket(dataset string, b Bucket, keep int) (int, error) {
	names, err := listBucketSnapshots(dataset, b)
	if err != nil {
		return 0, err
	}
	if len(names) <= keep {
		return 0, nil
	}
	excess := names[:len(names)-keep]
	for _, snap := range excess {
		if err := runCmd("zfs", "destroy", snap); err != nil {
			return 0, fmt.Errorf("destroy %s: %w", snap, err)
		}
	}
	return len(excess), nil
}

// listBucketSnapshots returns snapshots of `dataset` whose label starts with
// the bucket's prefix, sorted oldest first by creation time. Recursive
// listings (children) are deliberately excluded — auto-snapshot operates
// per-dataset (children get their own snapshots via property inheritance).
func listBucketSnapshots(dataset string, b Bucket) ([]string, error) {
	out, err := runOut("zfs", "list", "-H", "-t", "snapshot",
		"-o", "name", "-s", "creation", "-d", "1", dataset)
	if err != nil {
		return nil, err
	}
	prefix := dataset + "@" + b.SnapshotPrefix()
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			names = append(names, line)
		}
	}
	return names, nil
}

// pruneSelection is the pure-Go variant of pruneBucket's sort/select step,
// extracted for unit testing without touching zfs.
func pruneSelection(sortedOldestFirst []string, keep int) []string {
	if len(sortedOldestFirst) <= keep {
		return nil
	}
	out := make([]string, len(sortedOldestFirst)-keep)
	copy(out, sortedOldestFirst[:len(sortedOldestFirst)-keep])
	sort.Strings(out)
	return out
}

func runCmd(name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func runOut(name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
