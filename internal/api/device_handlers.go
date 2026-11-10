package api

import (
	"context"
	"fmt"
	"net/http"
	"runtime"

	"dumpstore/internal/blockdev"
	"dumpstore/internal/ops"
	"dumpstore/internal/zfs"
)

// getDevices handles GET /api/v1/devices
// Returns the physical block devices on this host, with in_use_by set to the
// pool name for devices that currently back a vdev (best-effort matching).
func (h *Handler) getDevices(context.Context, struct{}) ([]blockdev.Device, error) {
	devs, err := blockdev.List(runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("listing block devices: %w", err)
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
	return devs, nil
}

// replaceDevice handles POST /api/v1/pools/{pool}/devices/{device}/replace
// Body: { "new_device": "..." } — {device} is the device being replaced.
// Runs `zpool replace`, which starts a resilver onto the new device.
func (h *Handler) replaceDevice(ctx context.Context, in struct {
	poolDeviceParam
	Body ReplaceDeviceRequest `body:""`
}) (PoolResult, error) {
	pool := in.Pool
	req := struct{ OldDevice, NewDevice string }{in.Device, in.Body.NewDevice}
	if !validPoolName(pool) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	if !validVdevName(req.OldDevice) || !validVdevName(req.NewDevice) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid device identifier"))
	}
	if req.OldDevice == req.NewDevice {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("replacement device must differ from the old device"))
	}
	out, err := h.runLocal(ctx, ops.Step{
		Name: "Replace " + req.OldDevice + " with " + req.NewDevice + " in " + pool,
		Argv: []string{"zpool", "replace", pool, req.OldDevice, req.NewDevice},
	})
	auditLog(ctx, "pool.replace_device", pool+" "+req.OldDevice+" -> "+req.NewDevice, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	return PoolResult{Pool: pool, Tasks: out.Steps()}, nil
}

// ReplaceDeviceRequest is the POST /api/v1/pools/{pool}/devices/{device}/replace body.
type ReplaceDeviceRequest struct {
	NewDevice string `json:"new_device" doc:"Replacement device path"`
}

type deviceStateIn = poolDeviceParam

// offlineDevice handles POST /api/v1/pools/{pool}/devices/{device}/offline (no body).
func (h *Handler) offlineDevice(ctx context.Context, in deviceStateIn) (PoolResult, error) {
	return h.setDeviceState(ctx, in, "offline", "pool.device_offline")
}

// onlineDevice handles POST /api/v1/pools/{pool}/devices/{device}/online (no body).
func (h *Handler) onlineDevice(ctx context.Context, in deviceStateIn) (PoolResult, error) {
	return h.setDeviceState(ctx, in, "online", "pool.device_online")
}

// setDeviceState is the shared implementation of offlineDevice/onlineDevice.
func (h *Handler) setDeviceState(ctx context.Context, in deviceStateIn, subcmd, action string) (PoolResult, error) {
	pool, device := in.Pool, in.Device
	if !validPoolName(pool) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	if !validVdevName(device) {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid device identifier"))
	}
	out, err := h.runLocal(ctx, ops.Step{
		Name: "Take " + device + " " + subcmd + " in " + pool,
		Argv: []string{"zpool", subcmd, pool, device},
	})
	auditLog(ctx, action, pool+" "+device, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	return PoolResult{Pool: pool, Tasks: out.Steps()}, nil
}
