// Package jobs runs and tracks long-lived background processes.
//
// dumpstore's architectural rule is that idempotent configuration writes go
// through Ansible playbooks. Data-plane operations — long-running streaming
// transfers like `zfs send | zfs recv` — don't fit Ansible's request/response
// model: a single transfer can run for hours, far exceeding any reasonable
// HTTP timeout. The jobs package handles those: it spawns a child process
// (typically `bash -c "..."`), tracks status and output in memory, persists
// each job record to disk so state survives a restart, and exposes
// cancellation by signalling the child's process group.
package jobs

import "time"

// Status is the lifecycle state of a Job.
type Status string

const (
	StatusPending     Status = "pending"
	StatusRunning     Status = "running"
	StatusSuccess     Status = "success"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted" // service died while job was running
)

// Job is a serialisable snapshot of a tracked background execution.
//
// Stdout and Stderr are bounded ring-buffer tails (capped by Manager.tailSize),
// not the full output, so a multi-hour transfer cannot exhaust memory. Args
// includes the executable as element 0.
type Job struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Args       []string  `json:"args"`
	Status     Status    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	ExitCode   int       `json:"exit_code,omitempty"`
	Stdout     string    `json:"stdout,omitempty"`
	Stderr     string    `json:"stderr,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// terminal reports whether the job has reached an end state.
func (s Status) terminal() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	}
	return false
}
