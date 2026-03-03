package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/smart"
	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// apiError is returned as JSON for all non-2xx responses.
// Tasks is populated for Ansible-backed operations so the UI can show the op-log
// even when the request fails.
type apiError struct {
	Error string             `json:"error"`
	Tasks []ansible.TaskStep `json:"tasks,omitempty"`
}

// Handler holds dependencies for the HTTP API.
type Handler struct {
	runner  *ansible.Runner
	version string
}

// NewHandler creates a Handler with the given ansible Runner and app version string.
func NewHandler(runner *ansible.Runner, version string) *Handler {
	return &Handler{runner: runner, version: version}
}

// RegisterRoutes attaches all API routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sysinfo", h.getSysInfo)
	mux.HandleFunc("GET /api/version", h.getVersion)
	mux.HandleFunc("GET /api/poolstatus", h.getPoolStatuses)
	mux.HandleFunc("GET /api/pools", h.getPools)
	mux.HandleFunc("GET /api/datasets", h.getDatasets)
	mux.HandleFunc("POST /api/datasets", h.createDataset)
	mux.HandleFunc("DELETE /api/datasets/{name...}", h.deleteDataset)
	mux.HandleFunc("GET /metrics", h.getMetrics)
	mux.HandleFunc("GET /api/smart", h.getSMART)
	mux.HandleFunc("GET /api/dataset-props/{name...}", h.getDatasetProps)
	mux.HandleFunc("PATCH /api/datasets/{name...}", h.setDatasetProps)
	mux.HandleFunc("GET /api/snapshots", h.getSnapshots)
	mux.HandleFunc("GET /api/iostat", h.getIOStat)
	mux.HandleFunc("POST /api/snapshots", h.createSnapshot)
	mux.HandleFunc("DELETE /api/snapshots/{snapshot...}", h.deleteSnapshot)
}

func (h *Handler) getSysInfo(w http.ResponseWriter, r *http.Request) {
	type response struct {
		AppVersion string `json:"app_version"`
		system.Info
	}
	writeJSON(w, response{AppVersion: h.version, Info: system.Get()})
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	v, err := zfs.Version()
	if err != nil {
		slog.Warn("zpool version failed", "err", err)
		v = ""
	}
	writeJSON(w, map[string]string{"version": v})
}

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
	if strings.ContainsAny(name, "@;|&$`") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in dataset name"), nil)
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
	if strings.Contains(name, "@") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in dataset name"), nil)
		return
	}
	if strings.ContainsAny(name, ";|&$`") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in dataset name"), nil)
		return
	}

	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}

	allowed := []string{"compression", "quota", "mountpoint", "recordsize", "atime", "exec", "sync", "dedup", "copies", "xattr", "readonly"}
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
		if strings.ContainsAny(val, ";|&$`") {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in value for %s", prop), nil)
			return
		}
		vars[prop] = val
	}
	if len(vars) == 1 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no recognised properties to update"), nil)
		return
	}

	out, err := h.runner.Run("zfs_dataset_set.yml", vars)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	allFields := req.Name + req.VolSize + req.VolBlockSize + req.Compression +
		req.Quota + req.Mountpoint + req.RecordSize + req.Atime +
		req.Exec + req.Sync + req.Dedup + req.Copies + req.Xattr
	if strings.ContainsAny(allFields, "@;|&$`") {
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
	out, err := h.runner.Run("zfs_dataset_create.yml", vars)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Dataset == "" || req.SnapName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset and snapname are required"), nil)
		return
	}
	if strings.ContainsAny(req.Dataset+req.SnapName, "@;|&$`") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in dataset or snapname"), nil)
		return
	}

	recursive := "false"
	if req.Recursive {
		recursive = "true"
	}

	out, err := h.runner.Run("zfs_snapshot_create.yml", map[string]string{
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
	if strings.Contains(name, "@") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("use DELETE /api/snapshots to delete snapshots"), nil)
		return
	}
	if !strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("refusing to destroy a pool root"), nil)
		return
	}
	if strings.ContainsAny(name, ";|&$`") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in dataset name"), nil)
		return
	}

	recursive := "false"
	if r.URL.Query().Get("recursive") == "true" {
		recursive = "true"
	}

	out, err := h.runner.Run("zfs_dataset_destroy.yml", map[string]string{
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
	if !strings.Contains(snapshot, "@") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("snapshot must contain '@'"), nil)
		return
	}
	if strings.ContainsAny(snapshot, ";|&$`") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid characters in snapshot name"), nil)
		return
	}

	recursive := "false"
	if r.URL.Query().Get("recursive") == "true" {
		recursive = "true"
	}

	out, err := h.runner.Run("zfs_snapshot_destroy.yml", map[string]string{
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, err error, steps []ansible.TaskStep) {
	slog.Error("api error", "status", code, "err", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(apiError{Error: err.Error(), Tasks: steps})
}
