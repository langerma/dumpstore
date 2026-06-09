package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dumpstore/internal/zfs"
)

// vdevTypeMinDevices maps the accepted pool topologies to the minimum number
// of devices zpool requires for them (parity + 1 for raidz, data + parity for
// draid).
var vdevTypeMinDevices = map[string]int{
	"single": 1,
	"mirror": 2,
	"raidz1": 2,
	"raidz2": 3,
	"raidz3": 4,
	"draid":  2,
	"draid2": 3,
	"draid3": 4,
}

// createPool handles POST /api/pools
// Body: { "name": "tank", "vdev_type": "mirror", "devices": ["/dev/sdb", ...],
//
//	"ashift": "12", "compression": "zstd" }
//
// Creates a new pool via `zpool create`.
func (h *Handler) createPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string   `json:"name"`
		VdevType    string   `json:"vdev_type"`
		Devices     []string `json:"devices"`
		Ashift      string   `json:"ashift"`
		Compression string   `json:"compression"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if !validPoolName(req.Name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	minDevs, ok := vdevTypeMinDevices[req.VdevType]
	if !ok {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid vdev_type (single, mirror, raidz1-3, draid, draid2, draid3)"), nil)
		return
	}
	if len(req.Devices) < minDevs {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("%s requires at least %d devices", req.VdevType, minDevs), nil)
		return
	}
	for _, d := range req.Devices {
		if !validVdevName(d) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid device %q", d), nil)
			return
		}
	}
	if req.Ashift != "" {
		if v, err := strconv.Atoi(req.Ashift); err != nil || v < 9 || v > 16 {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("ashift must be between 9 and 16"), nil)
			return
		}
	}
	if req.Compression != "" && !reCompressionValue.MatchString(req.Compression) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid compression value"), nil)
		return
	}
	// Refuse to overwrite an existing pool of the same name.
	pools, err := zfs.ListPools()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	for _, p := range pools {
		if p.Name == req.Name {
			writeError(r.Context(), w, http.StatusConflict, fmt.Errorf("pool %q already exists", req.Name), nil)
			return
		}
	}

	out, err := h.runOp("zfs_pool_create.yml", map[string]string{
		"name":        req.Name,
		"vdev_type":   req.VdevType,
		"devices":     strings.Join(req.Devices, " "),
		"ashift":      req.Ashift,
		"compression": req.Compression,
	})
	auditLog(r.Context(), r, "pool.create", req.Name+" "+req.VdevType+" ["+strings.Join(req.Devices, " ")+"]", err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	h.publishDatasets()
	writeJSON(r.Context(), w, map[string]any{"pool": req.Name, "tasks": out.Steps()})
}

// getImportablePools handles GET /api/pools/importable
func (h *Handler) getImportablePools(w http.ResponseWriter, r *http.Request) {
	pools, err := zfs.ImportablePools()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, pools)
}

// importPool handles POST /api/pools/import
// Body: { "pool": "tank", "force": false }
func (h *Handler) importPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pool  string `json:"pool"`
		Force bool   `json:"force"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if !validPoolName(req.Pool) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	out, err := h.runOp("zfs_pool_import.yml", map[string]string{
		"pool":  req.Pool,
		"force": strconv.FormatBool(req.Force),
	})
	auditLog(r.Context(), r, "pool.import", req.Pool, err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	h.publishDatasets()
	writeJSON(r.Context(), w, map[string]any{"pool": req.Pool, "tasks": out.Steps()})
}

// exportPool handles POST /api/pools/{pool}/export
// Fails (surfaced via the op-log) when the pool is busy.
func (h *Handler) exportPool(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	if !validPoolName(pool) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	out, err := h.runOp("zfs_pool_export.yml", map[string]string{"pool": pool})
	auditLog(r.Context(), r, "pool.export", pool, err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	h.publishDatasets()
	writeJSON(r.Context(), w, map[string]any{"pool": pool, "tasks": out.Steps()})
}
