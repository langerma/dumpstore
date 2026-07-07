package api

import (
	"fmt"
	"net/http"
	"runtime"

	"dumpstore/internal/blockdev"
	"dumpstore/internal/ops"
	"dumpstore/internal/zfs"
)

// getDevices handles GET /api/devices
// Returns the physical block devices on this host, with in_use_by set to the
// pool name for devices that currently back a vdev (best-effort matching).
func (h *Handler) getDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := blockdev.List(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("listing block devices: %w", err), nil)
		return
	}
	if statuses, err := zfs.PoolStatuses(); err == nil {
		vdevByPool := make(map[string]string)
		for _, st := range statuses {
			for _, v := range st.Vdevs {
				if v.Depth >= 1 {
					vdevByPool[v.Name] = st.Name
				}
			}
		}
		blockdev.MarkInUse(devs, vdevByPool)
	}
	writeJSON(r.Context(), w, devs)
}

// replaceDevice handles POST /api/pools/{pool}/replace
// Body: { "old_device": "...", "new_device": "..." }
// Runs `zpool replace`, which starts a resilver onto the new device.
func (h *Handler) replaceDevice(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	if !validPoolName(pool) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	var req struct {
		OldDevice string `json:"old_device"`
		NewDevice string `json:"new_device"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if !validVdevName(req.OldDevice) || !validVdevName(req.NewDevice) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid device identifier"), nil)
		return
	}
	if req.OldDevice == req.NewDevice {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("replacement device must differ from the old device"), nil)
		return
	}
	out, err := h.runLocal(ops.Step{
		Name: "Replace " + req.OldDevice + " with " + req.NewDevice + " in " + pool,
		Argv: []string{"zpool", "replace", pool, req.OldDevice, req.NewDevice},
	})
	auditLog(r.Context(), r, "pool.replace_device", pool+" "+req.OldDevice+" -> "+req.NewDevice, err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	writeJSON(r.Context(), w, map[string]any{"pool": pool, "tasks": out.Steps()})
}

// offlineDevice handles POST /api/pools/{pool}/offline — body { "device": "..." }.
func (h *Handler) offlineDevice(w http.ResponseWriter, r *http.Request) {
	h.setDeviceState(w, r, "offline", "pool.device_offline")
}

// onlineDevice handles POST /api/pools/{pool}/online — body { "device": "..." }.
func (h *Handler) onlineDevice(w http.ResponseWriter, r *http.Request) {
	h.setDeviceState(w, r, "online", "pool.device_online")
}

// setDeviceState is the shared implementation of offlineDevice/onlineDevice.
func (h *Handler) setDeviceState(w http.ResponseWriter, r *http.Request, subcmd, action string) {
	pool := r.PathValue("pool")
	if !validPoolName(pool) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	var req struct {
		Device string `json:"device"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if !validVdevName(req.Device) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid device identifier"), nil)
		return
	}
	out, err := h.runLocal(ops.Step{
		Name: "Take " + req.Device + " " + subcmd + " in " + pool,
		Argv: []string{"zpool", subcmd, pool, req.Device},
	})
	auditLog(r.Context(), r, action, pool+" "+req.Device, err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	writeJSON(r.Context(), w, map[string]any{"pool": pool, "tasks": out.Steps()})
}
