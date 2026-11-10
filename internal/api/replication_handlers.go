package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dumpstore/internal/replication"
	"dumpstore/internal/scheduler"
)

// listReplication handles GET /api/v1/replication/tasks.
func (h *Handler) listReplication(context.Context, struct{}) ([]replication.Task, error) {
	tasks := h.repl.Store().List()
	// Strip LastRuns from the list view for brevity; clients fetch history
	// per-task via /api/v1/replication/tasks/{id}/history.
	for i := range tasks {
		tasks[i].LastRuns = nil
	}
	return tasks, nil
}

// replicationRequest is the shared body shape for POST and PATCH. All fields
// are pointers so PATCH can distinguish "not set" from "set to zero value".
type replicationRequest struct {
	Name           *string `json:"name"`
	Source         *string `json:"source"`
	Target         *string `json:"target"`
	Remote         *string `json:"remote"`
	Schedule       *string `json:"schedule"`
	RetentionCount *int    `json:"retention_count"`
	Raw            *bool   `json:"raw"`
	Recursive      *bool   `json:"recursive"`
	Enabled        *bool   `json:"enabled"`
}

func (req replicationRequest) validate(creating bool) error {
	check := func(label string, ptr *string, required bool, valid func(string) bool) error {
		if ptr == nil {
			if creating && required {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		}
		if *ptr == "" {
			if required {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		}
		if !valid(*ptr) {
			return fmt.Errorf("invalid %s", label)
		}
		return nil
	}
	if err := check("source", req.Source, true, validZFSName); err != nil {
		return err
	}
	if err := check("target", req.Target, true, func(s string) bool { return validZFSName(s) && strings.Contains(s, "/") }); err != nil {
		return err
	}
	if err := check("remote", req.Remote, false, validRemoteSpec); err != nil {
		return err
	}
	if req.Schedule != nil {
		if *req.Schedule == "" {
			return errors.New("schedule is required")
		}
		if _, err := scheduler.Parse(*req.Schedule); err != nil {
			return fmt.Errorf("invalid schedule: %w", err)
		}
	} else if creating {
		return errors.New("schedule is required")
	}
	if req.Name != nil && *req.Name == "" {
		return errors.New("name cannot be empty")
	}
	if creating && req.Name == nil {
		return errors.New("name is required")
	}
	if req.RetentionCount != nil && *req.RetentionCount < 0 {
		return errors.New("retention_count must be >= 0")
	}
	return nil
}

func (req replicationRequest) applyTo(t *replication.Task) {
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.Source != nil {
		t.Source = *req.Source
	}
	if req.Target != nil {
		t.Target = *req.Target
	}
	if req.Remote != nil {
		t.Remote = *req.Remote
	}
	if req.Schedule != nil {
		t.Schedule = *req.Schedule
	}
	if req.RetentionCount != nil {
		t.RetentionCount = *req.RetentionCount
	}
	if req.Raw != nil {
		t.Raw = *req.Raw
	}
	if req.Recursive != nil {
		t.Recursive = *req.Recursive
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
}

// createReplication handles POST /api/v1/replication/tasks.
func (h *Handler) createReplication(ctx context.Context, in struct {
	Body replicationRequest `body:""`
}) (replication.Task, error) {
	req := in.Body
	if err := req.validate(true); err != nil {
		return replication.Task{}, errStatus(http.StatusBadRequest, err)
	}
	// Default Enabled=true if caller didn't specify.
	if req.Enabled == nil {
		on := true
		req.Enabled = &on
	}
	if req.RetentionCount == nil {
		seven := 7
		req.RetentionCount = &seven
	}

	var task replication.Task
	req.applyTo(&task)
	h.refMu.Lock() // source/target must not be destroyed or renamed meanwhile (#183)
	defer h.refMu.Unlock()
	stored, err := h.repl.Store().Create(task)
	if err != nil {
		return replication.Task{}, fmt.Errorf("persist task: %w", err)
	}
	if err := h.repl.RegisterEnabled(stored); err != nil {
		// Roll back the persisted task — an invalid schedule must not silently leave a broken task.
		_ = h.repl.Store().Delete(stored.ID)
		return replication.Task{}, errStatus(http.StatusBadRequest, err)
	}
	auditLog(ctx, "replication.create", stored.ID, nil)
	h.broker.Publish("replication.update", h.repl.Store().List())
	return stored, nil
}

// updateReplication handles PATCH /api/v1/replication/tasks/{id}.
func (h *Handler) updateReplication(ctx context.Context, in struct {
	idParam
	Body replicationRequest `body:""`
}) (replication.Task, error) {
	id, req := in.ID, in.Body
	if id == "" {
		return replication.Task{}, errStatus(http.StatusBadRequest, errors.New("id required"))
	}
	if err := req.validate(false); err != nil {
		return replication.Task{}, errStatus(http.StatusBadRequest, err)
	}
	h.refMu.Lock() // source/target must not be destroyed or renamed meanwhile (#183)
	defer h.refMu.Unlock()
	updated, err := h.repl.Store().Update(id, func(t *replication.Task) { req.applyTo(t) })
	if err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			return replication.Task{}, errStatus(http.StatusNotFound, err)
		}
		return replication.Task{}, err
	}
	if err := h.repl.RegisterEnabled(updated); err != nil {
		return replication.Task{}, errStatus(http.StatusBadRequest, err)
	}
	auditLog(ctx, "replication.update", id, nil)
	h.broker.Publish("replication.update", h.repl.Store().List())
	return updated, nil
}

// deleteReplication handles DELETE /api/v1/replication/tasks/{id}.
func (h *Handler) deleteReplication(ctx context.Context, in idParam) (struct{}, error) {
	if in.ID == "" {
		return struct{}{}, errStatus(http.StatusBadRequest, errors.New("id required"))
	}
	h.repl.Unregister(in.ID)
	if err := h.repl.Store().Delete(in.ID); err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			return struct{}{}, errStatus(http.StatusNotFound, err)
		}
		return struct{}{}, err
	}
	auditLog(ctx, "replication.delete", in.ID, nil)
	h.broker.Publish("replication.update", h.repl.Store().List())
	return struct{}{}, nil
}

// runReplication handles POST /api/v1/replication/tasks/{id}/run.
func (h *Handler) runReplication(ctx context.Context, in idParam) (ReplicationRunStarted, error) {
	if in.ID == "" {
		return ReplicationRunStarted{}, errStatus(http.StatusBadRequest, errors.New("id required"))
	}
	// Detach from the request: RunOnce starts a goroutine that waits for the
	// job to finish, then releases the hold, prunes, and records history.
	// The request context is cancelled as soon as this handler returns.
	snap, jobID, err := h.repl.RunOnce(context.WithoutCancel(ctx), in.ID)
	if err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			return ReplicationRunStarted{}, errStatus(http.StatusNotFound, err)
		}
		return ReplicationRunStarted{}, err
	}
	auditLog(ctx, "replication.run", in.ID, nil)
	return ReplicationRunStarted{TaskID: in.ID, JobID: jobID, Snapshot: snap}, nil
}

// ReplicationRunStarted is the 202 response of a manual replication run.
type ReplicationRunStarted struct {
	TaskID   string `json:"task_id"`
	JobID    string `json:"job_id" doc:"Background job carrying the transfer; poll GET /api/v1/jobs/{id}"`
	Snapshot string `json:"snapshot" doc:"Snapshot taken for this run"`
}

// getReplicationHistory handles GET /api/v1/replication/tasks/{id}/history.
func (h *Handler) getReplicationHistory(_ context.Context, p idParam) ([]replication.RunRecord, error) {
	if p.ID == "" {
		return nil, errStatus(http.StatusBadRequest, errors.New("id required"))
	}
	t, err := h.repl.Store().Get(p.ID)
	if err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			return nil, errStatus(http.StatusNotFound, err)
		}
		return nil, err
	}
	runs := t.LastRuns
	if runs == nil {
		runs = []replication.RunRecord{}
	}
	return runs, nil
}
