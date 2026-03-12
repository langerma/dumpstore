package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/broker"
	"dumpstore/internal/smart"
	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// Input validation: whitelist regexes are stricter than a denylist and eliminate
// entire classes of injection (newlines, glob expansion, shell metacharacters, etc.).

var (
	// reZFSName matches a valid ZFS dataset/pool path: pool or pool/a/b/c
	// Components: letters, digits, underscore, hyphen, period, colon.
	reZFSName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]*(/[a-zA-Z0-9][a-zA-Z0-9_.:-]*)*$`)

	// reSnapLabel matches the label part of a snapshot name (after the '@').
	reSnapLabel = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$`)

	// reUnixName matches a valid POSIX username or group name.
	reUnixName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9._-]{0,31}$`)

	// reSMBShare matches a valid SMB share name (max 80 chars).
	reSMBShare = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,80}$`)

	// reShellPath matches a safe absolute filesystem path.
	reShellPath = regexp.MustCompile(`^/[a-zA-Z0-9/_.-]+$`)
)

// validZFSName returns true if s is a safe ZFS dataset/pool path (no snapshot suffix).
func validZFSName(s string) bool { return reZFSName.MatchString(s) }

// validSnapLabel returns true if s is a safe snapshot label (the part after '@').
func validSnapLabel(s string) bool { return reSnapLabel.MatchString(s) }

// validUnixName returns true if s is a valid POSIX username or group name.
func validUnixName(s string) bool { return reUnixName.MatchString(s) }

// validSMBShare returns true if s is a valid SMB share name.
func validSMBShare(s string) bool { return reSMBShare.MatchString(s) }

// validShellPath returns true if s is a safe absolute path.
func validShellPath(s string) bool { return reShellPath.MatchString(s) }

// safePropertyValue returns true if s contains no shell-dangerous or control
// characters. Used for dataset property values which are legitimately complex
// (e.g. sharenfs="rw=@10.0.0.0/24") and cannot be matched with a simple whitelist.
func safePropertyValue(s string) bool {
	return !strings.ContainsAny(s, ";\n\r`|&$*()?!~{}\\\"'")
}

// validUnixNameList returns true if s is empty or a comma-separated list of valid POSIX names.
func validUnixNameList(s string) bool {
	if s == "" {
		return true
	}
	for _, name := range strings.Split(s, ",") {
		if !validUnixName(strings.TrimSpace(name)) {
			return false
		}
	}
	return true
}

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
	broker  *broker.Broker
	userMu  sync.Mutex // serialises user/group write ops to avoid /etc/group lock contention
}

// NewHandler creates a Handler with the given ansible Runner, app version string,
// and subscription broker (used by the SSE endpoint).
func NewHandler(runner *ansible.Runner, version string, b *broker.Broker) *Handler {
	return &Handler{runner: runner, version: version, broker: b}
}

// runOp executes a playbook and publishes each task step to the ansible.progress
// SSE topic as it completes, so the frontend can show live progress.
func (h *Handler) runOp(playbook string, vars map[string]string) (*ansible.PlaybookOutput, error) {
	return h.runner.RunStreaming(playbook, vars, func(step ansible.TaskStep) {
		h.broker.PublishNoCache("ansible.progress", step)
	})
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
	mux.HandleFunc("GET /api/events", h.getEvents)
	mux.HandleFunc("GET /api/chown/{dataset...}", h.getDatasetOwnership)
	mux.HandleFunc("POST /api/chown/{dataset...}", h.setDatasetOwnership)
	mux.HandleFunc("GET /api/acl-status", h.getACLStatus)
	mux.HandleFunc("GET /api/acl/{dataset...}", h.getDatasetACL)
	mux.HandleFunc("POST /api/acl/{dataset...}", h.setACLEntry)
	mux.HandleFunc("DELETE /api/acl/{dataset...}", h.removeACLEntry)
	mux.HandleFunc("GET /api/users", h.getUsers)
	mux.HandleFunc("POST /api/users", h.createUser)
	mux.HandleFunc("PUT /api/users/{name}", h.modifyUser)
	mux.HandleFunc("DELETE /api/users/{name}", h.deleteUser)
	mux.HandleFunc("GET /api/groups", h.getGroups)
	mux.HandleFunc("POST /api/groups", h.createGroup)
	mux.HandleFunc("PUT /api/groups/{name}", h.modifyGroup)
	mux.HandleFunc("DELETE /api/groups/{name}", h.deleteGroup)
	mux.HandleFunc("GET /api/smb-users", h.getSambaUsers)
	mux.HandleFunc("POST /api/smb-users/{name}", h.addSambaUser)
	mux.HandleFunc("DELETE /api/smb-users/{name}", h.removeSambaUser)
	mux.HandleFunc("POST /api/smb-config/pam", h.configureSambaPAM)
	mux.HandleFunc("GET /api/smb-shares", h.getSMBShares)
	mux.HandleFunc("POST /api/smb-share/{dataset...}", h.setSMBShare)
	mux.HandleFunc("DELETE /api/smb-share/{dataset...}", h.deleteSMBShare)
	mux.HandleFunc("POST /api/scrub/{pool}", h.startScrub)
	mux.HandleFunc("DELETE /api/scrub/{pool}", h.cancelScrub)
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}

	allowed := []string{"compression", "quota", "mountpoint", "recordsize", "atime", "exec", "sync", "dedup", "copies", "xattr", "readonly", "acltype", "sharenfs", "sharesmb"}
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	var req struct {
		Owner     string `json:"owner"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

// getEvents handles GET /api/events?topics=pool.query,dataset.query,...
// It streams Server-Sent Events to the client until the connection is closed.
func (h *Handler) getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this transport"), nil)
		return
	}

	// Parse and validate requested topics; unknown names are silently ignored.
	var topics []string
	for _, t := range strings.Split(r.URL.Query().Get("topics"), ",") {
		t = strings.TrimSpace(t)
		if broker.ValidTopics[t] {
			topics = append(topics, t)
		}
	}
	if len(topics) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no valid topics requested"), nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // tell Nginx not to buffer this stream
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe to each requested topic.
	channels := make(map[string]chan []byte, len(topics))
	for _, t := range topics {
		channels[t] = h.broker.Subscribe(t)
	}
	defer func() {
		for t, ch := range channels {
			h.broker.Unsubscribe(t, ch)
		}
	}()

	// Fan-in: one goroutine per topic forwards messages into a single merged channel.
	// This avoids reflect.Select while keeping the main loop simple.
	type sseEvent struct {
		topic string
		data  []byte
	}
	merged := make(chan sseEvent, 16)
	var wg sync.WaitGroup
	ctx := r.Context()

	for topic, ch := range channels {
		wg.Add(1)
		go func(topic string, ch chan []byte) {
			defer wg.Done()
			for {
				select {
				case data, ok := <-ch:
					if !ok {
						return
					}
					select {
					case merged <- sseEvent{topic, data}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(topic, ch)
	}
	go func() { wg.Wait(); close(merged) }()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-merged:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.topic, ev.data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// getUsers handles GET /api/users
func (h *Handler) getUsers(w http.ResponseWriter, r *http.Request) {
	users, err := system.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, users)
}

// getGroups handles GET /api/groups
func (h *Handler) getGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := system.ListGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, groups)
}

// getSMBShares handles GET /api/smb-shares
// Returns all Samba usershares from `net usershare info *`.
func (h *Handler) getSMBShares(w http.ResponseWriter, r *http.Request) {
	shares, err := system.ListSMBUsershares()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, shares)
}

// setSMBShare handles POST /api/smb-share/{dataset...}
// Body: {"sharename":"myshare"}
// Creates a usershare via `net usershare add` using the dataset's mountpoint.
func (h *Handler) setSMBShare(w http.ResponseWriter, r *http.Request) {
	dataset := r.PathValue("dataset")
	if dataset == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(dataset) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	var req struct {
		Sharename string `json:"sharename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Sharename == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("sharename is required"), nil)
		return
	}
	if !validSMBShare(req.Sharename) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid sharename"), nil)
		return
	}
	out, err := h.runOp("smb_usershare_set.yml", map[string]string{
		"dataset":   dataset,
		"sharename": req.Sharename,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": dataset, "sharename": req.Sharename, "tasks": out.Steps()})
}

// deleteSMBShare handles DELETE /api/smb-share/{dataset...}?name=<sharename>
// Removes a usershare via `net usershare delete`.
func (h *Handler) deleteSMBShare(w http.ResponseWriter, r *http.Request) {
	dataset := r.PathValue("dataset")
	if dataset == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	sharename := r.URL.Query().Get("name")
	if sharename == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name query parameter required"), nil)
		return
	}
	if !validSMBShare(sharename) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid sharename"), nil)
		return
	}
	out, err := h.runOp("smb_usershare_unset.yml", map[string]string{
		"sharename": sharename,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": dataset, "sharename": sharename, "tasks": out.Steps()})
}

// getSambaUsers handles GET /api/smb-users
// Returns {"available":true,"users":["alice","bob"]} or {"available":false,"users":[]}
// when pdbedit is not installed.
func (h *Handler) getSambaUsers(w http.ResponseWriter, r *http.Request) {
	users, err := system.ListSambaUsers()
	if errors.Is(err, system.ErrSambaNotAvailable) {
		writeJSON(w, map[string]any{"available": false, "users": []string{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if users == nil {
		users = []string{}
	}
	writeJSON(w, map[string]any{"available": true, "users": users})
}

// addSambaUser handles POST /api/smb-users/{name}
// Body: {"password":"plaintext"}
func (h *Handler) addSambaUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("password is required"), nil)
		return
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("smb_user_add.yml", map[string]string{
		"username":     name,
		"smb_password": req.Password,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"username": name, "tasks": out.Steps()})
}

// removeSambaUser handles DELETE /api/smb-users/{name}
func (h *Handler) removeSambaUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("smb_user_remove.yml", map[string]string{
		"username": name,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"username": name, "tasks": out.Steps()})
}

// configureSambaPAM handles POST /api/smb-config/pam
// Applies usershare + PAM passthrough settings to /etc/samba/smb.conf and restarts smbd/nmbd.
func (h *Handler) configureSambaPAM(w http.ResponseWriter, r *http.Request) {
	out, err := h.runOp("smb_setup.yml", map[string]string{})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"tasks": out.Steps()})
}

// createUser handles POST /api/users
// Body: {"username":"alice","shell":"/bin/bash","uid":1001,"group":"storage","user_groups":"wheel,backup","password":"$6$...","create_group":true,"smb_user":false}
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Shell       string `json:"shell"`
		UID         string `json:"uid"`
		Group       string `json:"group"`
		Groups      string `json:"groups"`
		Password    string `json:"password"`
		CreateGroup bool   `json:"create_group"`
		SMBUser     bool   `json:"smb_user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("username is required"), nil)
		return
	}
	if req.Shell == "" {
		req.Shell = "/bin/bash"
	}
	if !validUnixName(req.Username) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	if !validShellPath(req.Shell) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid shell path"), nil)
		return
	}
	if req.Group != "" && !validUnixName(req.Group) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}
	if !validUnixNameList(req.Groups) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid supplementary group name"), nil)
		return
	}

	createGroup := "false"
	if req.CreateGroup {
		createGroup = "true"
	}
	smbUser := "false"
	if req.SMBUser {
		smbUser = "true"
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	vars := map[string]string{
		"username":     req.Username,
		"shell":        req.Shell,
		"uid":          req.UID,
		"group":        req.Group,
		"user_groups":  req.Groups,
		"password":     req.Password,
		"create_group": createGroup,
		"smb_user":     smbUser,
	}
	out, err := h.runOp("user_create.yml", vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"username": req.Username, "tasks": out.Steps()})
}

// protectedUsers lists accounts that must never be deleted regardless of UID.
var protectedUsers = map[string]bool{"nobody": true, "nfsnobody": true}

// protectedGroups lists groups that must never be deleted regardless of GID.
var protectedGroups = map[string]bool{"nogroup": true, "nobody": true, "nfsnobody": true}

// deleteUser handles DELETE /api/users/{name}
// Looks up the user's UID and rejects system users (uid < UIDMin).
func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}

	users, err := system.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	if protectedUsers[name] {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to delete protected user %q", name), nil)
		return
	}
	if target.UID < system.UIDMin() {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to delete system user (uid %d < %d)", target.UID, system.UIDMin()), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("user_delete.yml", map[string]string{
		"username": name,
		"uid":      fmt.Sprintf("%d", target.UID),
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(w, map[string]any{"username": name, "tasks": out.Steps()})
}

// modifyUser handles PUT /api/users/{name}
// Body: {"shell":"/bin/bash","group":"storage","user_groups":"wheel,backup","password":"$6$..."}
func (h *Handler) modifyUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	var req struct {
		Shell      string `json:"shell"`
		Group      string `json:"group"`
		UserGroups string `json:"user_groups"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Shell != "" && !validShellPath(req.Shell) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid shell path"), nil)
		return
	}
	if req.Group != "" && !validUnixName(req.Group) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}
	if !validUnixNameList(req.UserGroups) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid supplementary group name"), nil)
		return
	}

	users, err := system.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	if protectedUsers[name] {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to modify protected user %q", name), nil)
		return
	}
	if target.UID < system.UIDMin() {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to modify system user (uid %d < %d)", target.UID, system.UIDMin()), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("user_modify.yml", map[string]string{
		"username":      name,
		"uid":           fmt.Sprintf("%d", target.UID),
		"shell":         req.Shell,
		"primary_group": req.Group,
		"user_groups":   req.UserGroups,
		"password":      req.Password,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(w, map[string]any{"username": name, "tasks": out.Steps()})
}

// createGroup handles POST /api/groups
// Body: {"groupname":"storage","gid":"1500"}
func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Groupname string `json:"groupname"`
		GID       string `json:"gid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Groupname == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("groupname is required"), nil)
		return
	}
	if !validUnixName(req.Groupname) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("group_create.yml", map[string]string{
		"groupname": req.Groupname,
		"gid":       req.GID,
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
	h.publishUserGroup()
	_ = json.NewEncoder(w).Encode(map[string]any{"groupname": req.Groupname, "tasks": out.Steps()})
}

// deleteGroup handles DELETE /api/groups/{name}
// Looks up the group's GID and rejects system groups (gid < UIDMin).
func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("groupname required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}

	groups, err := system.ListGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.Group
	for i := range groups {
		if groups[i].Name == name {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("group %q not found", name), nil)
		return
	}
	if protectedGroups[name] {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to delete protected group %q", name), nil)
		return
	}
	if target.GID < system.UIDMin() {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to delete system group (gid %d < %d)", target.GID, system.UIDMin()), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("group_delete.yml", map[string]string{
		"groupname": name,
		"gid":       fmt.Sprintf("%d", target.GID),
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(w, map[string]any{"groupname": name, "tasks": out.Steps()})
}

// modifyGroup handles PUT /api/groups/{name}
// Body: {"new_name":"newgrp","gid":"1501","members":"alice,bob"}
func (h *Handler) modifyGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("groupname required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}
	var req struct {
		NewName string `json:"new_name"`
		GID     string `json:"gid"`
		Members string `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.NewName != "" && !validUnixName(req.NewName) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid new group name"), nil)
		return
	}
	if !validUnixNameList(req.Members) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid member name"), nil)
		return
	}

	groups, err := system.ListGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.Group
	for i := range groups {
		if groups[i].Name == name {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("group %q not found", name), nil)
		return
	}
	if protectedGroups[name] {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to modify protected group %q", name), nil)
		return
	}
	if target.GID < system.UIDMin() {
		writeError(w, http.StatusForbidden, fmt.Errorf("refusing to modify system group (gid %d < %d)", target.GID, system.UIDMin()), nil)
		return
	}

	resultName := name
	if req.NewName != "" {
		resultName = req.NewName
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("group_modify.yml", map[string]string{
		"groupname":      name,
		"gid":            fmt.Sprintf("%d", target.GID),
		"new_name":       req.NewName,
		"new_gid":        req.GID,
		"members":        req.Members,
		"update_members": "true",
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(w, map[string]any{"groupname": resultName, "tasks": out.Steps()})
}

// publishDatasets re-reads the dataset list and immediately pushes
// dataset.query to all SSE subscribers. Called after any dataset property change.
func (h *Handler) publishDatasets() {
	if datasets, err := zfs.ListDatasets(); err == nil {
		h.broker.Publish("dataset.query", datasets)
	}
}

// publishUserGroup re-reads /etc/passwd and /etc/group and immediately pushes
// both topics to all SSE subscribers. Called after every successful user/group
// write so clients see the change without waiting for the next poller tick.
func (h *Handler) publishUserGroup() {
	if users, err := system.ListUsers(); err == nil {
		h.broker.Publish("user.query", users)
	}
	if groups, err := system.ListGroups(); err == nil {
		h.broker.Publish("group.query", groups)
	}
}

// aclSafeRe matches strings that are safe to pass as ACE specs to setfacl / nfs4_setfacl.
// Allows letters, digits, and the small set of punctuation found in valid ACE specs:
//   - POSIX: "user:alice:rwx", "default:group:storage:r-x"
//   - NFSv4: "A::OWNER@:rwaDxtTnNcCoy", "A:fd:alice@localdomain:rwx"
var aclSafeRe = func() func(string) bool {
	return func(s string) bool {
		for _, c := range s {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == ':' || c == '@' || c == '-' || c == '_' || c == '.' || c == '=' || c == '/') {
				return false
			}
		}
		return len(s) > 0
	}
}()

// getACLStatus handles GET /api/acl-status
// Returns a map of dataset name → bool indicating whether the dataset has
// non-trivial ACL entries on its mountpoint.
func (h *Handler) getACLStatus(w http.ResponseWriter, r *http.Request) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	status := make(map[string]bool, len(datasets))
	for _, d := range datasets {
		if d.Type != "filesystem" || d.Mountpoint == "none" || d.Mountpoint == "-" || d.Mountpoint == "" {
			continue
		}
		// DatasetHasACL checks actual getfacl output; fall back to acltype
		// when getfacl / nfs4_getfacl are not available.
		hasACL := zfs.DatasetHasACL(d.Mountpoint)
		if !hasACL {
			hasACL = d.ACLType != "" && d.ACLType != "off"
		}
		status[d.Name] = hasACL
	}
	writeJSON(w, status)
}

// getDatasetACL handles GET /api/acl/{dataset...}
func (h *Handler) getDatasetACL(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, acl)
}

// setACLEntry handles POST /api/acl/{dataset...}
// Body: {"ace":"user:alice:rwx","recursive":false}  (POSIX)
//
//	{"ace":"A::alice@localdomain:rwaDxtTnNcCoy"}       (NFSv4)
func (h *Handler) setACLEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}

	var req struct {
		ACE       string `json:"ace"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.ACE == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("ace is required"), nil)
		return
	}
	if !aclSafeRe(req.ACE) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("ace contains invalid characters"), nil)
		return
	}

	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if acl.ACLType == "off" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("acltype is off for dataset %s; enable it first", name), nil)
		return
	}
	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name), nil)
		return
	}

	recursive := "false"
	if req.Recursive {
		recursive = "true"
	}

	var playbook string
	vars := map[string]string{
		"dataset":    name,
		"mountpoint": acl.Mountpoint,
		"ace":        req.ACE,
		"recursive":  recursive,
	}
	switch acl.ACLType {
	case "posix":
		playbook = "acl_set_posix.yml"
	case "nfsv4":
		playbook = "acl_set_nfs4.yml"
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported acltype: %s", acl.ACLType), nil)
		return
	}

	out, err := h.runOp(playbook, vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": name, "ace": req.ACE, "tasks": out.Steps()})
}

// removeACLEntry handles DELETE /api/acl/{dataset...}?entry=<spec>
// For POSIX: entry is the removal spec (e.g. "user:alice", "default:group:storage")
// For NFSv4: entry is the full ACE string to remove
func (h *Handler) removeACLEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}

	entry := r.URL.Query().Get("entry")
	if entry == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("entry query parameter required"), nil)
		return
	}
	if !aclSafeRe(entry) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("entry contains invalid characters"), nil)
		return
	}

	recursive := r.URL.Query().Get("recursive") == "true"

	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name), nil)
		return
	}

	recursiveStr := "false"
	if recursive {
		recursiveStr = "true"
	}

	var playbook string
	vars := map[string]string{
		"mountpoint": acl.Mountpoint,
		"ace":        entry,
		"recursive":  recursiveStr,
	}
	switch acl.ACLType {
	case "posix":
		playbook = "acl_remove_posix.yml"
	case "nfsv4":
		playbook = "acl_remove_nfs4.yml"
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported acltype: %s", acl.ACLType), nil)
		return
	}

	out, err := h.runOp(playbook, vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": name, "entry": entry, "tasks": out.Steps()})
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
