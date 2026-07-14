package api

import (
	"fmt"
	"net/http"
	"time"

	"dumpstore/internal/autosnap"
)

// autoSnapshotStatusResponse is the body of GET /api/auto-snapshot/status.
type autoSnapshotStatusResponse struct {
	autosnap.Status
	LastRuns map[autosnap.Bucket]time.Time `json:"last_runs,omitempty"`
}

// getAutoSnapshotStatus reports whether the OS daemon is active and whether
// dumpstore's scheduler tasks are currently registered.
func (h *Handler) getAutoSnapshotStatus(w http.ResponseWriter, r *http.Request) {
	status := autosnap.DetectStatus()
	if h.autosnap != nil {
		status.DumpstoreManaged = h.autosnap.IsRegistered()
	}
	resp := autoSnapshotStatusResponse{Status: status}
	if h.autosnap != nil {
		resp.LastRuns = h.autosnap.LastRuns()
	}
	writeJSON(r.Context(), w, resp)
}

// takeoverAutoSnapshot disables the OS daemon then registers dumpstore's
// bucket tasks with the scheduler. Returns 200 + ansible task steps.
func (h *Handler) takeoverAutoSnapshot(w http.ResponseWriter, r *http.Request) {
	if h.autosnap == nil {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("autosnap runner not initialised"), nil)
		return
	}
	out, err := h.runOp(r.Context(), "auto_snapshot_takeover.yml", map[string]string{})
	auditLog(r.Context(), r, "auto_snapshot.takeover", "", err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	if err := h.autosnap.Register(); err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("register scheduler tasks: %w", err), nil)
		return
	}
	h.publishAutoSnapStatus()
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// releaseAutoSnapshot unregisters dumpstore's tasks and re-enables the OS daemon.
func (h *Handler) releaseAutoSnapshot(w http.ResponseWriter, r *http.Request) {
	if h.autosnap == nil {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("autosnap runner not initialised"), nil)
		return
	}
	h.autosnap.Unregister()
	out, err := h.runOp(r.Context(), "auto_snapshot_release.yml", map[string]string{})
	auditLog(r.Context(), r, "auto_snapshot.release", "", err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	h.publishAutoSnapStatus()
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// publishAutoSnapStatus pushes the fresh status to all SSE subscribers.
func (h *Handler) publishAutoSnapStatus() {
	status := autosnap.DetectStatus()
	if h.autosnap != nil {
		status.DumpstoreManaged = h.autosnap.IsRegistered()
	}
	h.broker.Publish("autosnap.status", status)
}
