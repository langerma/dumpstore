package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/schema"
	"dumpstore/internal/smart"
	"dumpstore/internal/zfs"
)

func (h *Handler) getPoolStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := zfs.PoolStatuses()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, statuses)
}

func (h *Handler) getPools(w http.ResponseWriter, r *http.Request) {
	pools, err := zfs.ListPools()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, pools)
}

func (h *Handler) getDatasets(w http.ResponseWriter, r *http.Request) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, datasets)
}

// getSMART handles GET /api/smart
func (h *Handler) getSMART(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, smart.Collect())
}

// getDatasetProps handles GET /api/dataset-props/{name...}
func (h *Handler) getDatasetProps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	props, err := zfs.GetDatasetProps(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, props)
}

// setDatasetProps handles PATCH /api/datasets/{name...}
// Body: a JSON object with any subset of editable properties.
// Empty string value means "zfs inherit" (reset to inherited); non-empty means "zfs set".
// Only the properties listed in the allowed set are forwarded to the playbook;
// unknown properties in the request body are silently ignored.
func (h *Handler) setDatasetProps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}

	var body map[string]string
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}

	allowed := schema.AllowedNames()
	allowedSet := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		allowedSet[p] = true
	}

	// Start with just the dataset name; add only allowed, validated properties.
	vars := map[string]string{"name": name}
	for prop, val := range body {
		if !allowedSet[prop] {
			continue
		}
		if !safePropertyValue(val) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in value for %s", prop), nil)
			return
		}
		vars[prop] = val
	}
	if len(vars) == 1 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no recognised properties to update"), nil)
		return
	}
	ok, err := datasetExists(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dataset %q not found", name), nil)
		return
	}

	out, err := h.runOp("zfs_dataset_set.yml", vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishDatasets()
	writeJSON(w, map[string]any{"name": name, "tasks": out.Steps()})
}

// createDataset handles POST /api/datasets
// Body: {"name":"tank/data","type":"filesystem","compression":"lz4","quota":"","mountpoint":""}
// For volumes also: {"volsize":"10G"}
func (h *Handler) createDataset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		VolSize      string `json:"volsize"`
		VolBlockSize string `json:"volblocksize"`
		Sparse       bool   `json:"sparse"`
		Compression  string `json:"compression"`
		Quota        string `json:"quota"`
		Mountpoint   string `json:"mountpoint"`
		RecordSize   string `json:"recordsize"`
		Atime        string `json:"atime"`
		Exec         string `json:"exec"`
		Sync         string `json:"sync"`
		Dedup        string `json:"dedup"`
		Copies       string `json:"copies"`
		Xattr        string `json:"xattr"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Name == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name and type are required"), nil)
		return
	}
	if req.Type != "filesystem" && req.Type != "volume" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("type must be filesystem or volume"), nil)
		return
	}
	if req.Type == "volume" && req.VolSize == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("volsize is required for volumes"), nil)
		return
	}
	if !validZFSName(req.Name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	if req.Mountpoint != "" && !validShellPath(req.Mountpoint) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid mountpoint path"), nil)
		return
	}
	propFields := req.VolSize + req.VolBlockSize + req.Compression +
		req.Quota + req.RecordSize + req.Atime +
		req.Exec + req.Sync + req.Dedup + req.Copies + req.Xattr
	if !safePropertyValue(propFields) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in request"), nil)
		return
	}

	sparse := "false"
	if req.Sparse {
		sparse = "true"
	}
	vars := map[string]string{
		"name":         req.Name,
		"type":         req.Type,
		"volsize":      req.VolSize,
		"volblocksize": req.VolBlockSize,
		"sparse":       sparse,
		"compression":  req.Compression,
		"quota":        req.Quota,
		"mountpoint":   req.Mountpoint,
		"recordsize":   req.RecordSize,
		"atime":        req.Atime,
		"exec":         req.Exec,
		"sync":         req.Sync,
		"dedup":        req.Dedup,
		"copies":       req.Copies,
		"xattr":        req.Xattr,
	}
	out, err := h.runOp("zfs_dataset_create.yml", vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":  req.Name,
		"type":  req.Type,
		"tasks": out.Steps(),
	})
}

func (h *Handler) getSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := zfs.ListSnapshots()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, snaps)
}

func (h *Handler) getIOStat(w http.ResponseWriter, r *http.Request) {
	stats, err := zfs.IOStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, stats)
}

// createSnapshot handles POST /api/snapshots
// Body: {"dataset":"tank/data","snapname":"backup","recursive":false}
func (h *Handler) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dataset   string `json:"dataset"`
		SnapName  string `json:"snapname"`
		Recursive bool   `json:"recursive"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Dataset == "" || req.SnapName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset and snapname are required"), nil)
		return
	}
	if !validZFSName(req.Dataset) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	if !validSnapLabel(req.SnapName) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid snapshot label"), nil)
		return
	}

	recursive := "false"
	if req.Recursive {
		recursive = "true"
	}

	out, err := h.runOp("zfs_snapshot_create.yml", map[string]string{
		"dataset":   req.Dataset,
		"snapname":  req.SnapName,
		"recursive": recursive,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshot": req.Dataset + "@" + req.SnapName,
		"created":  time.Now().UTC().Format(time.RFC3339),
		"tasks":    out.Steps(),
	})
}

// deleteDataset handles DELETE /api/datasets/{name}
// Pool roots (names without a '/') are rejected — use `zpool destroy` for that.
// Names containing '@' are rejected — use DELETE /api/snapshots instead.
// Pass ?recursive=true to also destroy all child datasets and snapshots.
func (h *Handler) deleteDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	if !strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("refusing to destroy a pool root"), nil)
		return
	}
	ok, err := datasetExists(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dataset %q not found", name), nil)
		return
	}

	recursive := "false"
	if r.URL.Query().Get("recursive") == "true" {
		recursive = "true"
	}

	out, err := h.runOp("zfs_dataset_destroy.yml", map[string]string{
		"name":      name,
		"recursive": recursive,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}

	writeJSON(w, map[string]any{"name": name, "tasks": out.Steps()})
}

// deleteSnapshot handles DELETE /api/snapshots/{dataset@snapname}
func (h *Handler) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot := r.PathValue("snapshot")
	if snapshot == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("snapshot path required"), nil)
		return
	}
	parts := strings.SplitN(snapshot, "@", 2)
	if len(parts) != 2 || !validZFSName(parts[0]) || !validSnapLabel(parts[1]) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid snapshot name (expected dataset@label)"), nil)
		return
	}

	recursive := "false"
	if r.URL.Query().Get("recursive") == "true" {
		recursive = "true"
	}

	out, err := h.runOp("zfs_snapshot_destroy.yml", map[string]string{
		"snapshot":  snapshot,
		"recursive": recursive,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}

	writeJSON(w, map[string]any{"snapshot": snapshot, "tasks": out.Steps()})
}

// deleteSnapshotBatch handles POST /api/snapshots/delete-batch
// Body: {"snapshots": ["tank/data@snap1", "tank/home@snap2"]}
func (h *Handler) deleteSnapshotBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Snapshots []string `json:"snapshots"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"), nil)
		return
	}
	if len(body.Snapshots) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("snapshots list is empty"), nil)
		return
	}
	if len(body.Snapshots) > 100 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("too many snapshots (max 100)"), nil)
		return
	}
	for _, snap := range body.Snapshots {
		parts := strings.SplitN(snap, "@", 2)
		if len(parts) != 2 || !validZFSName(parts[0]) || !validSnapLabel(parts[1]) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid snapshot name %q (expected dataset@label)", snap), nil)
			return
		}
	}

	snapsJSON, err := json.Marshal(body.Snapshots)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("encoding snapshots: %w", err), nil)
		return
	}

	out, err := h.runOp("zfs_snapshot_destroy_batch.yml", map[string]string{
		"snapshots_json": string(snapsJSON),
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"snapshots": body.Snapshots, "tasks": out.Steps()})
}

// getDatasetOwnership handles GET /api/chown/{dataset...}
// Returns the current owner and group of the dataset's mountpoint directory.
func (h *Handler) getDatasetOwnership(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	props, err := zfs.GetDatasetProps(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	mp := props["mountpoint"].Value
	if mp == "none" || mp == "-" || mp == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name), nil)
		return
	}
	owner, group, err := zfs.GetMountpointOwnership(mp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, map[string]string{
		"dataset":    name,
		"mountpoint": mp,
		"owner":      owner,
		"group":      group,
	})
}

// setDatasetOwnership handles POST /api/chown/{dataset...}
// Body: {"owner":"alice","group":"storage","recursive":false}
func (h *Handler) setDatasetOwnership(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	if ok, err := datasetExists(name); err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dataset %q not found", name), nil)
		return
	}

	var req struct {
		Owner     string `json:"owner"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Owner == "" || req.Group == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("owner and group are required"), nil)
		return
	}
	if !validUnixName(req.Owner) || !validUnixName(req.Group) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid owner or group name"), nil)
		return
	}

	props, err := zfs.GetDatasetProps(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	mp := props["mountpoint"].Value
	if mp == "none" || mp == "-" || mp == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name), nil)
		return
	}

	recursive := "false"
	if req.Recursive {
		recursive = "true"
	}

	out, err := h.runOp("dataset_chown.yml", map[string]string{
		"mountpoint": mp,
		"owner":      req.Owner,
		"group":      req.Group,
		"recursive":  recursive,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": name, "tasks": out.Steps()})
}

// publishDatasets re-reads the dataset list and immediately pushes
// dataset.query to all SSE subscribers. Called after any dataset property change.
func (h *Handler) publishDatasets() {
	if datasets, err := zfs.ListDatasets(); err == nil {
		h.broker.Publish("dataset.query", datasets)
	}
}

// startScrub handles POST /api/scrub/{pool}
// Initiates a scrub on the named pool via `zpool scrub`.
func (h *Handler) startScrub(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	if pool == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("pool name required"), nil)
		return
	}
	if !validZFSName(pool) || strings.Contains(pool, "/") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	out, err := h.runOp("zfs_scrub_start.yml", map[string]string{"pool": pool})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"pool": pool, "tasks": out.Steps()})
}

// cancelScrub handles DELETE /api/scrub/{pool}
// Cancels a running scrub on the named pool via `zpool scrub -s`.
func (h *Handler) cancelScrub(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	if pool == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("pool name required"), nil)
		return
	}
	if !validZFSName(pool) || strings.Contains(pool, "/") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}
	out, err := h.runOp("zfs_scrub_cancel.yml", map[string]string{"pool": pool})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"pool": pool, "tasks": out.Steps()})
}

// listScrubSchedules handles GET /api/scrub-schedules
// Returns mode + schedules from the platform cron source (Linux) or
// /etc/periodic.conf (FreeBSD).
func (h *Handler) listScrubSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := zfs.ScrubSchedules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("reading scrub schedules: %w", err), nil)
		return
	}
	writeJSON(w, list)
}

// setScrubSchedule handles PUT /api/scrub-schedule/{pool}
// Linux:   adds pool to ZFS_SCRUB_POOLS in /etc/default/zfs.
// FreeBSD: adds pool to daily_scrub_zfs_pools in /etc/periodic.conf and sets
//
//	the scrub threshold (threshold_days, default 35).
func (h *Handler) setScrubSchedule(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	if pool == "" || !validZFSName(pool) || strings.Contains(pool, "/") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}

	if zfs.OSType() == "freebsd" {
		var req struct {
			ThresholdDays int `json:"threshold_days"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req) // threshold_days optional
		if req.ThresholdDays <= 0 {
			req.ThresholdDays = 35
		}
		out, err := h.runOp("zfs_scrub_periodic_enable.yml", map[string]string{
			"pool":           pool,
			"threshold_days": strconv.Itoa(req.ThresholdDays),
		})
		if err != nil {
			var steps []ansible.TaskStep
			if out != nil {
				steps = out.Steps()
			}
			writeError(w, http.StatusInternalServerError, err, steps)
			return
		}
		writeJSON(w, map[string]any{"pool": pool, "tasks": out.Steps()})
		return
	}

	// Linux: add to ZFS_SCRUB_POOLS — no schedule params required.
	out, err := h.runOp("zfs_scrub_zfsutils_enable.yml", map[string]string{"pool": pool})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"pool": pool, "tasks": out.Steps()})
}

// deleteScrubSchedule handles DELETE /api/scrub-schedule/{pool}
// Linux:   removes pool from ZFS_SCRUB_POOLS in /etc/default/zfs.
// FreeBSD: removes pool from daily_scrub_zfs_pools in /etc/periodic.conf.
func (h *Handler) deleteScrubSchedule(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	if pool == "" || !validZFSName(pool) || strings.Contains(pool, "/") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid pool name"), nil)
		return
	}

	playbook := "zfs_scrub_zfsutils_disable.yml"
	if zfs.OSType() == "freebsd" {
		playbook = "zfs_scrub_periodic_disable.yml"
	}

	out, err := h.runOp(playbook, map[string]string{"pool": pool})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"pool": pool, "tasks": out.Steps()})
}

// listAutoSnapshotSchedules handles GET /api/auto-snapshot-schedules
func (h *Handler) listAutoSnapshotSchedules(w http.ResponseWriter, r *http.Request) {
	props, err := zfs.ListAutoSnapshotProps()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, props)
}

// publishAutoSnapshot re-reads all auto-snapshot props and pushes autosnapshot.query
// to all SSE subscribers. Called after any auto-snapshot property change.
func (h *Handler) publishAutoSnapshot() {
	if props, err := zfs.ListAutoSnapshotProps(); err == nil {
		h.broker.Publish("autosnapshot.query", props)
	}
}

// autoSnapProps maps the 6 com.sun:auto-snapshot* property names to their
// Ansible-safe variable names (colons and dots replaced with underscores).
var autoSnapProps = []struct{ prop, varName string }{
	{"com.sun:auto-snapshot", "com_sun_auto_snapshot"},
	{"com.sun:auto-snapshot:frequent", "com_sun_auto_snapshot_frequent"},
	{"com.sun:auto-snapshot:hourly", "com_sun_auto_snapshot_hourly"},
	{"com.sun:auto-snapshot:daily", "com_sun_auto_snapshot_daily"},
	{"com.sun:auto-snapshot:weekly", "com_sun_auto_snapshot_weekly"},
	{"com.sun:auto-snapshot:monthly", "com_sun_auto_snapshot_monthly"},
}

var reKeepCount = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)

// validAutoSnapValue returns true if val is a valid value for the given
// com.sun:auto-snapshot* property. Empty string means "inherit".
func validAutoSnapValue(prop, val string) bool {
	if val == "" {
		return true
	}
	if prop == "com.sun:auto-snapshot" {
		return val == "true" || val == "false"
	}
	return reKeepCount.MatchString(val)
}

// getAutoSnapshotProps handles GET /api/auto-snapshot/{name...}
func (h *Handler) getAutoSnapshotProps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	props, err := zfs.GetAutoSnapshotProps(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, props)
}

// setAutoSnapshotProps handles PUT /api/auto-snapshot/{name...}
func (h *Handler) setAutoSnapshotProps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}

	var body map[string]string
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"), nil)
		return
	}

	// Build allowed-prop set for fast lookup.
	allowed := make(map[string]string, len(autoSnapProps))
	for _, p := range autoSnapProps {
		allowed[p.prop] = p.varName
	}

	vars := map[string]string{"name": name}
	for _, ap := range autoSnapProps {
		val, ok := body[ap.prop]
		if !ok {
			continue // not provided — playbook default (__skip__) applies
		}
		if !validAutoSnapValue(ap.prop, val) {
			writeError(w, http.StatusBadRequest,
				fmt.Errorf("invalid value %q for property %s", val, ap.prop), nil)
			return
		}
		vars[ap.varName] = val
	}

	out, err := h.runOp("zfs_autosnap_set.yml", vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishDatasets()
	h.publishAutoSnapshot()
	writeJSON(w, map[string]any{"name": name, "tasks": out.Steps()})
}
