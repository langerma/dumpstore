package api

import (
	"context"
	"dumpstore/internal/jobs"
	"fmt"
	"net/http"
)

// listJobs handles GET /api/v1/jobs
func (h *Handler) listJobs(context.Context, struct{}) ([]jobs.Job, error) {
	return h.jobs.List(), nil
}

// getJob handles GET /api/v1/jobs/{id}
func (h *Handler) getJob(_ context.Context, p idParam) (jobs.Job, error) {
	j, ok := h.jobs.Get(p.ID)
	if !ok {
		return j, errStatus(http.StatusNotFound, fmt.Errorf("job %q not found", p.ID))
	}
	return j, nil
}

// cancelJob handles POST /api/v1/jobs/{id}/cancel
func (h *Handler) cancelJob(ctx context.Context, in idParam) (struct{}, error) {
	if err := h.jobs.Cancel(in.ID); err != nil {
		return struct{}{}, errStatus(http.StatusBadRequest, err)
	}
	auditLog(ctx, "job.cancel", in.ID, nil)
	return struct{}{}, nil
}

// removeJob handles DELETE /api/v1/jobs/{id}
// Drops a terminal job from the list and from disk. Refuses to remove a
// running job — clients must cancel it first.
func (h *Handler) removeJob(ctx context.Context, in idParam) (struct{}, error) {
	if err := h.jobs.Remove(in.ID); err != nil {
		return struct{}{}, errStatus(http.StatusBadRequest, err)
	}
	auditLog(ctx, "job.remove", in.ID, nil)
	return struct{}{}, nil
}
