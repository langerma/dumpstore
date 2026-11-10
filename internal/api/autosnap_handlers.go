package api

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"dumpstore/internal/autosnap"
	"dumpstore/internal/platform"
)

// autoSnapshotStatusResponse is the body of GET /api/v1/auto-snapshot/status.
type autoSnapshotStatusResponse struct {
	autosnap.Status
	LastRuns map[autosnap.Bucket]time.Time `json:"last_runs,omitempty"`
}

// getAutoSnapshotStatus reports whether the OS daemon is active and whether
// dumpstore's scheduler tasks are currently registered.
func (h *Handler) getAutoSnapshotStatus(context.Context, struct{}) (autoSnapshotStatusResponse, error) {
	status := autosnap.DetectStatus()
	if h.autosnap != nil {
		status.DumpstoreManaged = h.autosnap.IsRegistered()
	}
	resp := autoSnapshotStatusResponse{Status: status}
	if h.autosnap != nil {
		resp.LastRuns = h.autosnap.LastRuns()
	}
	return resp, nil
}

// autosnapPlaybookVars carries where takeover records what it disabled —
// the systemd timers (Linux) and the periodic.conf settings (FreeBSD) — so
// release restores exactly those.
func autosnapPlaybookVars() map[string]string {
	dir := platform.StateDir(runtime.GOOS)
	return map[string]string{
		"timer_state_file":    filepath.Join(dir, "autosnap-disabled-timers"),
		"periodic_state_file": filepath.Join(dir, "autosnap-disabled-periodic.json"),
	}
}

// takeoverAutoSnapshot disables the OS daemon then registers dumpstore's
// bucket tasks with the scheduler. Returns 200 + ansible task steps.
func (h *Handler) takeoverAutoSnapshot(ctx context.Context, _ struct{}) (TaskResult, error) {
	if h.autosnap == nil {
		return TaskResult{}, fmt.Errorf("autosnap runner not initialised")
	}
	out, err := h.runOp(ctx, "auto_snapshot_takeover.yml", autosnapPlaybookVars())
	auditLog(ctx, "auto_snapshot.takeover", "", err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	if err := h.autosnap.Register(); err != nil {
		return TaskResult{}, fmt.Errorf("register scheduler tasks: %w", err)
	}
	h.publishAutoSnapStatus()
	return TaskResult{Tasks: out.Steps()}, nil
}

// releaseAutoSnapshot unregisters dumpstore's tasks and re-enables the OS daemon.
func (h *Handler) releaseAutoSnapshot(ctx context.Context, _ struct{}) (TaskResult, error) {
	if h.autosnap == nil {
		return TaskResult{}, fmt.Errorf("autosnap runner not initialised")
	}
	h.autosnap.Unregister()
	out, err := h.runOp(ctx, "auto_snapshot_release.yml", autosnapPlaybookVars())
	auditLog(ctx, "auto_snapshot.release", "", err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	h.publishAutoSnapStatus()
	return TaskResult{Tasks: out.Steps()}, nil
}

// publishAutoSnapStatus pushes the fresh status to all SSE subscribers.
func (h *Handler) publishAutoSnapStatus() {
	status := autosnap.DetectStatus()
	if h.autosnap != nil {
		status.DumpstoreManaged = h.autosnap.IsRegistered()
	}
	h.broker.Publish("autosnap.status", status)
}
