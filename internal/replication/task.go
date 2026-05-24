// Package replication implements scheduled ZFS replication tasks.
//
// A task is a persisted description of `zfs send | zfs recv` work that runs
// on a cron schedule. The package owns the source snapshots it creates
// (named `dumpstore-repl-*`) and the destination snapshots it copied over;
// it does not touch snapshots created by other tools.
//
// When a task fires, the runner:
//  1. Creates a source snapshot named `dumpstore-repl-<UTC>`.
//  2. Places a `dumpstore-repl` hold on it so other tools cannot prune it
//     mid-transfer.
//  3. Finds the most recent `dumpstore-repl-*` snapshot common to source and
//     destination for incremental selection.
//  4. Dispatches `zfs send | zfs recv` via the jobs manager.
//  5. Releases the hold and, on success, prunes destination replication
//     snapshots beyond the configured retention count.
//
// All execution surfaces in the standard Jobs tab tagged
// `replication.run` / `replication.prune`.
package replication

import "time"

// HoldTag is the constant tag dumpstore puts on snapshots it creates during a
// replication run. Exported so test fixtures and admin tooling can recognise
// and clear these holds if a job is interrupted between hold and release.
const HoldTag = "dumpstore-repl"

// SnapshotPrefix is the prefix of every source snapshot replication creates.
// Pruning, incremental selection, and "is this ours" decisions all use it.
const SnapshotPrefix = "dumpstore-repl-"

// MaxRunHistory bounds Task.LastRuns to avoid unbounded growth in
// replication.json. 20 is plenty to debug recent failures.
const MaxRunHistory = 20

// Task is one persisted replication configuration.
type Task struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Source         string      `json:"source"`           // local dataset, e.g. tank/data
	Target         string      `json:"target"`           // dataset path on destination
	Remote         string      `json:"remote,omitempty"` // user@host; empty = local
	Schedule       string      `json:"schedule"`         // 5-field cron expression
	RetentionCount int         `json:"retention_count"`  // dest dumpstore-repl-* snapshots to keep
	Raw            bool        `json:"raw"`              // pass --raw to zfs send (encrypted datasets)
	Recursive      bool        `json:"recursive"`        // snapshot/send -r
	Enabled        bool        `json:"enabled"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	LastRuns       []RunRecord `json:"last_runs,omitempty"`
}

// RunRecord is one entry in Task.LastRuns.
type RunRecord struct {
	JobID      string    `json:"job_id"`
	Snapshot   string    `json:"snapshot"`    // source snapshot created for this run
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Status     string    `json:"status"` // "success" | "failed" | "cancelled" | "interrupted"
	Error      string    `json:"error,omitempty"`
}
