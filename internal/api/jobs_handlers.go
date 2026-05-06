package api

import (
	"fmt"
	"net/http"
)

// listJobs handles GET /api/jobs
func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, h.jobs.List())
}

// getJob handles GET /api/jobs/{id}
func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, ok := h.jobs.Get(id)
	if !ok {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("job %q not found", id), nil)
		return
	}
	writeJSON(r.Context(), w, j)
}

// cancelJob handles POST /api/jobs/{id}/cancel
func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.jobs.Cancel(id); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, err, nil)
		return
	}
	auditLog(r.Context(), r, "job.cancel", id, nil)
	w.WriteHeader(http.StatusNoContent)
}
