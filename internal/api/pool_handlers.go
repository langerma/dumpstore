package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dumpstore/internal/ops"
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

	// "single" (striped, no redundancy) passes devices without a vdev
	// keyword; every other accepted type is a valid zpool keyword as-is.
	argv := []string{"zpool", "create"}
	if req.Ashift != "" {
		argv = append(argv, "-o", "ashift="+req.Ashift)
	}
	if req.Compression != "" {
		argv = append(argv, "-O", "compression="+req.Compression)
	}
	argv = append(argv, req.Name)
	if req.VdevType != "single" {
		argv = append(argv, req.VdevType)
	}
	argv = append(argv, req.Devices...)
	out, err := h.runLocal(ops.Step{Name: "Create pool " + req.Name, Argv: argv})
	auditLog(r.Context(), r, "pool.create", req.Name+" "+req.VdevType+" ["+strings.Join(req.Devices, " ")+"]", err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	h.publishDatasets()
	writeJSON(r.Context(), w, map[string]any{"pool": req.Name, "tasks": out.Steps()})
}

// addDataVdev handles POST /api/pools/{pool}/vdevs
// Body: { "vdev_type": "mirror", "devices": ["/dev/sdd", "/dev/sde"] }
// Adding a data vdev is irreversible on most pool layouts — the UI gates this
// behind confirm-by-typing.
func (h *Handler) addDataVdev(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	var req struct {
		VdevType string   `json:"vdev_type"`
		Devices  []string `json:"devices"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	minDevs, ok := vdevTypeMinDevices[req.VdevType]
	if !ok || strings.HasPrefix(req.VdevType, "draid") {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid vdev_type (single, mirror, raidz1-3)"), nil)
		return
	}
	if len(req.Devices) < minDevs {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("%s requires at least %d devices", req.VdevType, minDevs), nil)
		return
	}
	h.addPoolDevices(w, r, pool, "data", req.VdevType, req.Devices)
}

// addCacheDevice handles POST /api/pools/{pool}/cache — body { "devices": [...] }.
func (h *Handler) addCacheDevice(w http.ResponseWriter, r *http.Request) {
	h.addAuxDevices(w, r, "cache")
}

// addSpareDevice handles POST /api/pools/{pool}/spare — body { "devices": [...] }.
func (h *Handler) addSpareDevice(w http.ResponseWriter, r *http.Request) {
	h.addAuxDevices(w, r, "spare")
}

// addLogDevice handles POST /api/pools/{pool}/log
// Body: { "devices": [...], "mirror": bool }
func (h *Handler) addLogDevice(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	var req struct {
		Devices []string `json:"devices"`
		Mirror  bool     `json:"mirror"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	vdevType := ""
	if req.Mirror {
		if len(req.Devices) < 2 {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("a mirrored log requires at least 2 devices"), nil)
			return
		}
		vdevType = "mirror"
	}
	h.addPoolDevices(w, r, pool, "log", vdevType, req.Devices)
}

// addAuxDevices is the shared body for cache and spare additions.
func (h *Handler) addAuxDevices(w http.ResponseWriter, r *http.Request, kind string) {
	pool := r.PathValue("pool")
	var req struct {
		Devices []string `json:"devices"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	h.addPoolDevices(w, r, pool, kind, "", req.Devices)
}

// addPoolDevices validates and dispatches a `zpool add`.
func (h *Handler) addPoolDevices(w http.ResponseWriter, r *http.Request, pool, kind, vdevType string, devices []string) {
	if !validPoolName(pool) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	if len(devices) == 0 {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("at least one device required"), nil)
		return
	}
	for _, d := range devices {
		if !validVdevName(d) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid device %q", d), nil)
			return
		}
	}
	// Data vdevs take an optional topology keyword; cache/log/spare use the
	// zpool class keyword, with log additionally allowing a mirror keyword.
	argv := []string{"zpool", "add", pool}
	switch kind {
	case "data":
		if vdevType != "" && vdevType != "single" {
			argv = append(argv, vdevType)
		}
	case "log":
		argv = append(argv, "log")
		if vdevType == "mirror" {
			argv = append(argv, "mirror")
		}
	default: // cache, spare
		argv = append(argv, kind)
	}
	argv = append(argv, devices...)
	out, err := h.runLocal(ops.Step{Name: "Add " + kind + " devices to " + pool, Argv: argv})
	auditLog(r.Context(), r, "pool.add_"+kind, pool+" ["+strings.Join(devices, " ")+"]", err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	writeJSON(r.Context(), w, map[string]any{"pool": pool, "tasks": out.Steps()})
}

// removePoolDevice handles DELETE /api/pools/{pool}/devices/{device...}
// Removes a cache, log, or spare device via `zpool remove`. (zpool itself
// decides whether the removal is legal for the given vdev.)
func (h *Handler) removePoolDevice(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	device := r.PathValue("device")
	if !validPoolName(pool) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	if !validVdevName(device) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid device identifier"), nil)
		return
	}
	out, err := h.runLocal(ops.Step{
		Name: "Remove " + device + " from " + pool,
		Argv: []string{"zpool", "remove", pool, device},
	})
	auditLog(r.Context(), r, "pool.remove_device", pool+" "+device, err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	writeJSON(r.Context(), w, map[string]any{"pool": pool, "tasks": out.Steps()})
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
	argv := []string{"zpool", "import"}
	if req.Force {
		argv = append(argv, "-f")
	}
	argv = append(argv, req.Pool)
	out, err := h.runLocal(ops.Step{Name: "Import pool " + req.Pool, Argv: argv})
	auditLog(r.Context(), r, "pool.import", req.Pool, err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
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
	out, err := h.runLocal(ops.Step{Name: "Export pool " + pool, Argv: []string{"zpool", "export", pool}})
	auditLog(r.Context(), r, "pool.export", pool, err)
	if err != nil {
		writeOpsError(r.Context(), w, err, out)
		return
	}
	h.publishPools()
	h.publishDatasets()
	writeJSON(r.Context(), w, map[string]any{"pool": pool, "tasks": out.Steps()})
}
