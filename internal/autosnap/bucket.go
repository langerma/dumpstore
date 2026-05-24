// Package autosnap implements the `com.sun:auto-snapshot:*` property model
// natively, replacing the OS daemons `zfs-auto-snapshot` (Linux) and
// `zfstools` (FreeBSD).
//
// The properties remain the source of truth and are inheritance-correct
// because `zfs get` already resolves inheritance for us. On Linux this is
// equivalent to what `zfs-auto-snapshot` does. On FreeBSD it fixes #74 —
// upstream `zfstools` silently skips datasets that inherit
// `com.sun:auto-snapshot=true` from a parent.
//
// Snapshot naming follows the legacy convention
// (`zfs-auto-snap_<bucket>-YYYY-MM-DD-HHMM`) so pre-existing snapshots are
// recognised for retention pruning.
package autosnap

import (
	"strconv"
	"time"
)

// Bucket is one of the five standard auto-snapshot cadences.
type Bucket string

const (
	BucketFrequent Bucket = "frequent"
	BucketHourly   Bucket = "hourly"
	BucketDaily    Bucket = "daily"
	BucketWeekly   Bucket = "weekly"
	BucketMonthly  Bucket = "monthly"
)

// AllBuckets is the canonical iteration order.
var AllBuckets = []Bucket{
	BucketFrequent,
	BucketHourly,
	BucketDaily,
	BucketWeekly,
	BucketMonthly,
}

// CronExpression returns the 5-field cron expression dumpstore uses to fire
// the bucket. Mirrors the systemd timer / cron defaults shipped with
// `zfs-auto-snapshot` so the cutover from daemon to dumpstore is
// behaviourally invisible.
func (b Bucket) CronExpression() string {
	switch b {
	case BucketFrequent:
		return "*/15 * * * *" // every 15 minutes
	case BucketHourly:
		return "0 * * * *" // top of hour
	case BucketDaily:
		return "0 0 * * *" // midnight UTC
	case BucketWeekly:
		return "0 0 * * 0" // Sunday midnight
	case BucketMonthly:
		return "0 0 1 * *" // 1st of month
	}
	return ""
}

// SnapshotPrefix returns the literal prefix every snapshot in this bucket
// shares (everything before the timestamp). Used for filtering snapshots
// belonging to the bucket — both for retention pruning and to recognise
// snapshots created by the legacy OS daemon.
func (b Bucket) SnapshotPrefix() string {
	return "zfs-auto-snap_" + string(b) + "-"
}

// SnapshotLabel returns the full snapshot label (the part after `@`) for a
// snapshot created at `now` in this bucket. Format matches `zfs-auto-snapshot`
// so prior snapshots and new ones share a sort order. UTC is used (rather
// than local) for unambiguous ordering across DST transitions; legacy
// snapshots created in local time still sort correctly *within* their TZ.
func (b Bucket) SnapshotLabel(now time.Time) string {
	return b.SnapshotPrefix() + now.UTC().Format("2006-01-02-1504")
}

// keepFromValue parses a per-bucket property value into a positive integer
// keep-count. Returns (0, false) for empty / "-" / non-numeric / zero values
// — i.e. the bucket is treated as disabled in those cases.
//
// This matches dumpstore's existing convention (see validAutoSnapValue in
// internal/api/zfs_handlers.go): per-bucket values are positive integers,
// the master `com.sun:auto-snapshot` boolean gates the whole thing.
func keepFromValue(v string) (int, bool) {
	if v == "" || v == "-" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
