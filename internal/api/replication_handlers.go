package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dumpstore/internal/replication"
	"dumpstore/internal/scheduler"
)

// listReplication handles GET /api/replication.
func (h *Handler) listReplication(w http.ResponseWriter, r *http.Request) {
	tasks := h.repl.Store().List()
	// Strip LastRuns from the list view for brevity; clients fetch history
	// per-task via /api/replication/{id}/history.
	for i := range tasks {
		tasks[i].LastRuns = nil
	}
	writeJSON(r.Context(), w, tasks)
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

// createReplication handles POST /api/replication.
func (h *Handler) createReplication(w http.ResponseWriter, r *http.Request) {
	var req replicationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if err := req.validate(true); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, err, nil)
		return
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
	stored, err := h.repl.Store().Create(task)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("persist task: %w", err), nil)
		return
	}
	if err := h.repl.RegisterEnabled(stored); err != nil {
		// Roll back the persisted task — an invalid schedule must not silently leave a broken task.
		_ = h.repl.Store().Delete(stored.ID)
		writeError(r.Context(), w, http.StatusBadRequest, err, nil)
		return
	}
	auditLog(r.Context(), r, "replication.create", stored.ID, nil)
	h.broker.Publish("replication.update", h.repl.Store().List())
	w.WriteHeader(http.StatusCreated)
	writeJSON(r.Context(), w, stored)
}

// updateReplication handles PATCH /api/replication/{id}.
func (h *Handler) updateReplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("id required"), nil)
		return
	}
	var req replicationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if err := req.validate(false); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, err, nil)
		return
	}
	updated, err := h.repl.Store().Update(id, func(t *replication.Task) { req.applyTo(t) })
	if err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			writeError(r.Context(), w, http.StatusNotFound, err, nil)
			return
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	if err := h.repl.RegisterEnabled(updated); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, err, nil)
		return
	}
	auditLog(r.Context(), r, "replication.update", id, nil)
	h.broker.Publish("replication.update", h.repl.Store().List())
	writeJSON(r.Context(), w, updated)
}

// deleteReplication handles DELETE /api/replication/{id}.
func (h *Handler) deleteReplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("id required"), nil)
		return
	}
	h.repl.Unregister(id)
	if err := h.repl.Store().Delete(id); err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			writeError(r.Context(), w, http.StatusNotFound, err, nil)
			return
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	auditLog(r.Context(), r, "replication.delete", id, nil)
	h.broker.Publish("replication.update", h.repl.Store().List())
	w.WriteHeader(http.StatusNoContent)
}

// runReplication handles POST /api/replication/{id}/run.
func (h *Handler) runReplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("id required"), nil)
		return
	}
	snap, jobID, err := h.repl.RunOnce(r.Context(), id)
	if err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			writeError(r.Context(), w, http.StatusNotFound, err, nil)
			return
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	auditLog(r.Context(), r, "replication.run", id, nil)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(r.Context(), w, map[string]any{
		"task_id":  id,
		"job_id":   jobID,
		"snapshot": snap,
	})
}

// getReplicationHistory handles GET /api/replication/{id}/history.
func (h *Handler) getReplicationHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("id required"), nil)
		return
	}
	t, err := h.repl.Store().Get(id)
	if err != nil {
		if errors.Is(err, replication.ErrNotFound) {
			writeError(r.Context(), w, http.StatusNotFound, err, nil)
			return
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	runs := t.LastRuns
	if runs == nil {
		runs = []replication.RunRecord{}
	}
	writeJSON(r.Context(), w, runs)
}
