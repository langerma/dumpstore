package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dumpstore/internal/ansible"
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

// CreatePoolRequest is the POST /api/v1/pools body.
type CreatePoolRequest struct {
	Name        string   `json:"name"`
	VdevType    string   `json:"vdev_type" enum:"single,mirror,raidz1,raidz2,raidz3,draid,draid2,draid3"`
	Devices     []string `json:"devices"`
	Ashift      string   `json:"ashift,omitempty" doc:"9–16; empty for the zpool default"`
	Compression string   `json:"compression,omitempty" doc:"Root dataset compression; empty for the default"`
}

// AddVdevRequest is the POST /api/v1/pools/{pool}/vdevs body.
type AddVdevRequest struct {
	VdevType string   `json:"vdev_type" enum:"single,mirror,raidz1,raidz2,raidz3"`
	Devices  []string `json:"devices"`
}

// AddDevicesRequest is the body for adding cache or spare devices.
type AddDevicesRequest struct {
	Devices []string `json:"devices"`
}

// AddLogRequest is the POST /api/v1/pools/{pool}/log body.
type AddLogRequest struct {
	Devices []string `json:"devices"`
	Mirror  bool     `json:"mirror,omitempty"`
}

// ImportPoolRequest is the POST /api/v1/pools/import body.
type ImportPoolRequest struct {
	Pool  string `json:"pool"`
	Force bool   `json:"force,omitempty"`
}

// PoolResult is the result of a write on one pool: its name and the
// op-log steps.
type PoolResult struct {
	Pool  string             `json:"pool"`
	Tasks []ansible.TaskStep `json:"tasks"`
}

// createPool handles POST /api/v1/pools
// Body: { "name": "tank", "vdev_type": "mirror", "devices": ["/dev/sdb", ...],
//
//	"ashift": "12", "compression": "zstd" }
//
// Creates a new pool via `zpool create`.
func (h *Handler) createPool(ctx context.Context, in struct {
	Body CreatePoolRequest `body:""`
}) (PoolResult, error) {
	req := in.Body
	if !validPoolName(req.Name) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	minDevs, ok := vdevTypeMinDevices[req.VdevType]
	if !ok {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid vdev_type (single, mirror, raidz1-3, draid, draid2, draid3)"))
	}
	if len(req.Devices) < minDevs {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("%s requires at least %d devices", req.VdevType, minDevs))
	}
	for _, d := range req.Devices {
		if !validVdevName(d) {
			return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid device %q", d))
		}
	}
	if req.Ashift != "" {
		if v, err := strconv.Atoi(req.Ashift); err != nil || v < 9 || v > 16 {
			return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("ashift must be between 9 and 16"))
		}
	}
	if req.Compression != "" && !reCompressionValue.MatchString(req.Compression) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid compression value"))
	}
	// Refuse to overwrite an existing pool of the same name.
	pools, err := zfs.ListPools()
	if err != nil {
		return PoolResult{}, err
	}
	for _, p := range pools {
		if p.Name == req.Name {
			return PoolResult{}, errStatus(http.StatusConflict, fmt.Errorf("pool %q already exists", req.Name))
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
	out, err := h.runLocal(ctx, ops.Step{Name: "Create pool " + req.Name, Argv: argv})
	auditLog(ctx, "pool.create", req.Name+" "+req.VdevType+" ["+strings.Join(req.Devices, " ")+"]", err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	h.publishDatasets()
	return PoolResult{Pool: req.Name, Tasks: out.Steps()}, nil
}

// addDataVdev handles POST /api/v1/pools/{pool}/vdevs
// Body: { "vdev_type": "mirror", "devices": ["/dev/sdd", "/dev/sde"] }
// Adding a data vdev is irreversible on most pool layouts — the UI gates this
// behind confirm-by-typing.
func (h *Handler) addDataVdev(ctx context.Context, in struct {
	poolParam
	Body AddVdevRequest `body:""`
}) (PoolResult, error) {
	minDevs, ok := vdevTypeMinDevices[in.Body.VdevType]
	if !ok || strings.HasPrefix(in.Body.VdevType, "draid") {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid vdev_type (single, mirror, raidz1-3)"))
	}
	if len(in.Body.Devices) < minDevs {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("%s requires at least %d devices", in.Body.VdevType, minDevs))
	}
	return h.addPoolDevices(ctx, in.Pool, "data", in.Body.VdevType, in.Body.Devices)
}

// addCacheDevice handles POST /api/v1/pools/{pool}/cache — body { "devices": [...] }.
func (h *Handler) addCacheDevice(ctx context.Context, in struct {
	poolParam
	Body AddDevicesRequest `body:""`
}) (PoolResult, error) {
	return h.addPoolDevices(ctx, in.Pool, "cache", "", in.Body.Devices)
}

// addSpareDevice handles POST /api/v1/pools/{pool}/spare — body { "devices": [...] }.
func (h *Handler) addSpareDevice(ctx context.Context, in struct {
	poolParam
	Body AddDevicesRequest `body:""`
}) (PoolResult, error) {
	return h.addPoolDevices(ctx, in.Pool, "spare", "", in.Body.Devices)
}

// addLogDevice handles POST /api/v1/pools/{pool}/log
// Body: { "devices": [...], "mirror": bool }
func (h *Handler) addLogDevice(ctx context.Context, in struct {
	poolParam
	Body AddLogRequest `body:""`
}) (PoolResult, error) {
	vdevType := ""
	if in.Body.Mirror {
		if len(in.Body.Devices) < 2 {
			return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("a mirrored log requires at least 2 devices"))
		}
		vdevType = "mirror"
	}
	return h.addPoolDevices(ctx, in.Pool, "log", vdevType, in.Body.Devices)
}

// addPoolDevices validates and dispatches a `zpool add`.
func (h *Handler) addPoolDevices(ctx context.Context, pool, kind, vdevType string, devices []string) (PoolResult, error) {
	if !validPoolName(pool) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	if len(devices) == 0 {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("at least one device required"))
	}
	for _, d := range devices {
		if !validVdevName(d) {
			return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid device %q", d))
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
	out, err := h.runLocal(ctx, ops.Step{Name: "Add " + kind + " devices to " + pool, Argv: argv})
	auditLog(ctx, "pool.add_"+kind, pool+" ["+strings.Join(devices, " ")+"]", err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	return PoolResult{Pool: pool, Tasks: out.Steps()}, nil
}

// removePoolDevice handles DELETE /api/v1/pools/{pool}/devices/{device}
// Removes a cache, log, or spare device via `zpool remove`. (zpool itself
// decides whether the removal is legal for the given vdev.)
func (h *Handler) removePoolDevice(ctx context.Context, in poolDeviceParam) (PoolResult, error) {
	if !validPoolName(in.Pool) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	if !validVdevName(in.Device) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid device identifier"))
	}
	out, err := h.runLocal(ctx, ops.Step{
		Name: "Remove " + in.Device + " from " + in.Pool,
		Argv: []string{"zpool", "remove", in.Pool, in.Device},
	})
	auditLog(ctx, "pool.remove_device", in.Pool+" "+in.Device, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	return PoolResult{Pool: in.Pool, Tasks: out.Steps()}, nil
}

// getImportablePools handles GET /api/v1/pools/importable
func (h *Handler) getImportablePools(context.Context, struct{}) ([]zfs.ImportablePool, error) {
	return zfs.ImportablePools()
}

// importPool handles POST /api/v1/pools/import
// Body: { "pool": "tank", "force": false }
func (h *Handler) importPool(ctx context.Context, in struct {
	Body ImportPoolRequest `body:""`
}) (PoolResult, error) {
	req := in.Body
	if !validPoolName(req.Pool) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	argv := []string{"zpool", "import"}
	if req.Force {
		argv = append(argv, "-f")
	}
	argv = append(argv, req.Pool)
	out, err := h.runLocal(ctx, ops.Step{Name: "Import pool " + req.Pool, Argv: argv})
	auditLog(ctx, "pool.import", req.Pool, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	h.publishDatasets()
	return PoolResult{Pool: req.Pool, Tasks: out.Steps()}, nil
}

// exportPool handles POST /api/v1/pools/{pool}/export
// Fails (surfaced via the op-log) when the pool is busy.
func (h *Handler) exportPool(ctx context.Context, in poolParam) (PoolResult, error) {
	if !validPoolName(in.Pool) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	out, err := h.runLocal(ctx, ops.Step{Name: "Export pool " + in.Pool, Argv: []string{"zpool", "export", in.Pool}})
	auditLog(ctx, "pool.export", in.Pool, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	h.publishDatasets()
	return PoolResult{Pool: in.Pool, Tasks: out.Steps()}, nil
}
