package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/ops"
	"dumpstore/internal/schema"
	"dumpstore/internal/smart"
	"dumpstore/internal/zfs"
)

func (h *Handler) getPoolStatuses(context.Context, struct{}) ([]zfs.PoolDetail, error) {
	return zfs.PoolStatuses()
}

func (h *Handler) getPools(context.Context, struct{}) ([]zfs.Pool, error) {
	return zfs.ListPools()
}

func (h *Handler) getDatasets(context.Context, struct{}) ([]zfs.Dataset, error) {
	return zfs.ListDatasets()
}

// getSMART handles GET /api/smart
func (h *Handler) getSMART(context.Context, struct{}) (smart.Result, error) {
	return smart.Collect(), nil
}

// getDatasetProps handles GET /api/dataset-props/{name...}
func (h *Handler) getDatasetProps(_ context.Context, p datasetNameParam) (map[string]zfs.DatasetProp, error) {
	if p.Name == "" {
		return nil, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(p.Name) {
		return nil, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	return zfs.GetDatasetProps(p.Name)
}

// setDatasetProps handles PATCH /api/datasets/{name...}
// Body: a JSON object with any subset of editable properties.
// Empty string value means "zfs inherit" (reset to inherited); non-empty means "zfs set".
// Only the properties listed in the allowed set are forwarded to the playbook;
// unknown properties in the request body are silently ignored.
func (h *Handler) setDatasetProps(ctx context.Context, in struct {
	datasetNameParam
	Body map[string]string `body:""`
}) (DatasetResult, error) {
	name := in.Name
	if name == "" {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(name) {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}

	allowed := schema.AllowedNames()
	allowedSet := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		allowedSet[p] = true
	}

	sizeProps := map[string]bool{
		"quota": true, "refquota": true,
		"reservation": true, "refreservation": true,
	}

	// Start with just the dataset name; add only allowed, validated properties.
	vars := map[string]string{"name": name}
	for prop, val := range in.Body {
		if !allowedSet[prop] {
			continue
		}
		if !safePropertyValue(val) {
			return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid characters in value for %s", prop))
		}
		if sizeProps[prop] && val != "" && !validZFSSize(val) {
			return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid value for %s: must be a positive size value (e.g. 10G) or 'none'", prop))
		}
		vars[prop] = val
	}
	if len(vars) == 1 {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("no recognised properties to update"))
	}
	// A mountpoint change moves paths that shares may reference (#183).
	if mp, ok := vars["mountpoint"]; ok {
		h.refMu.Lock()
		defer h.refMu.Unlock()
		if err := h.checkDatasetRefs(refMountpoint, name, false, mp); err != nil {
			return DatasetResult{}, err
		}
	} else if ok, err := datasetExists(name); err != nil {
		return DatasetResult{}, err
	} else if !ok {
		return DatasetResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}

	out, err := h.runOp(ctx, "zfs_dataset_set.yml", vars)
	auditLog(ctx, "dataset.modify", name, err)
	if err != nil {
		return DatasetResult{}, playbookError(err, out)
	}
	h.publishDatasets()
	return DatasetResult{Name: name, Tasks: out.Steps()}, nil
}

// DatasetResult is the result of a write on one dataset: its name and the
// op-log steps.
type DatasetResult struct {
	Name  string             `json:"name"`
	Tasks []ansible.TaskStep `json:"tasks"`
}

// DatasetTaskResult is DatasetResult keyed "dataset" instead of "name".
type DatasetTaskResult struct {
	Dataset string             `json:"dataset"`
	Tasks   []ansible.TaskStep `json:"tasks"`
}

// CreateDatasetRequest is the POST /api/datasets body. Empty optional
// properties are left to ZFS defaults / inheritance.
type CreateDatasetRequest struct {
	Name         string `json:"name"`
	Type         string `json:"type" enum:"filesystem,volume"`
	VolSize      string `json:"volsize,omitempty" doc:"Required for volumes, e.g. 10G"`
	VolBlockSize string `json:"volblocksize,omitempty"`
	Sparse       bool   `json:"sparse,omitempty" doc:"Volumes: thin-provisioned (no reservation)"`
	Compression  string `json:"compression,omitempty"`
	Quota        string `json:"quota,omitempty"`
	Mountpoint   string `json:"mountpoint,omitempty"`
	RecordSize   string `json:"recordsize,omitempty"`
	Atime        string `json:"atime,omitempty"`
	Exec         string `json:"exec,omitempty"`
	Sync         string `json:"sync,omitempty"`
	Dedup        string `json:"dedup,omitempty"`
	Copies       string `json:"copies,omitempty"`
	Xattr        string `json:"xattr,omitempty"`
}

// CreateDatasetResult is the POST /api/datasets response.
type CreateDatasetResult struct {
	Name  string             `json:"name"`
	Type  string             `json:"type"`
	Tasks []ansible.TaskStep `json:"tasks"`
}

// createDataset handles POST /api/datasets
// Body: {"name":"tank/data","type":"filesystem","compression":"lz4","quota":"","mountpoint":""}
// For volumes also: {"volsize":"10G"}
func (h *Handler) createDataset(ctx context.Context, in struct {
	Body CreateDatasetRequest `body:""`
}) (CreateDatasetResult, error) {
	req := in.Body
	if req.Name == "" || req.Type == "" {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("name and type are required"))
	}
	if req.Type != "filesystem" && req.Type != "volume" {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("type must be filesystem or volume"))
	}
	if req.Type == "volume" && req.VolSize == "" {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("volsize is required for volumes"))
	}
	if !validZFSName(req.Name) {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if req.Mountpoint != "" && !validShellPath(req.Mountpoint) {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid mountpoint path"))
	}
	propFields := req.VolSize + req.VolBlockSize + req.Compression +
		req.Quota + req.RecordSize + req.Atime +
		req.Exec + req.Sync + req.Dedup + req.Copies + req.Xattr
	if !safePropertyValue(propFields) {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid characters in request"))
	}
	if req.VolSize != "" && !validZFSSize(req.VolSize) {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid volsize: must be a positive size value (e.g. 10G)"))
	}
	if req.Quota != "" && !validZFSSize(req.Quota) {
		return CreateDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid quota: must be a positive size value (e.g. 10G) or 'none'"))
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
	out, err := h.runOp(ctx, "zfs_dataset_create.yml", vars)
	auditLog(ctx, "dataset.create", req.Name, err)
	if err != nil {
		return CreateDatasetResult{}, playbookError(err, out)
	}

	h.publishDatasets()
	return CreateDatasetResult{Name: req.Name, Type: req.Type, Tasks: out.Steps()}, nil
}

func (h *Handler) getSnapshots(context.Context, struct{}) ([]zfs.Snapshot, error) {
	return zfs.ListSnapshots()
}

func (h *Handler) getIOStat(context.Context, struct{}) ([]zfs.IOStat, error) {
	return zfs.IOStats()
}

// createSnapshot handles POST /api/snapshots
// Body: {"dataset":"tank/data","snapname":"backup","recursive":false}
func (h *Handler) createSnapshot(ctx context.Context, in struct {
	Body CreateSnapshotRequest `body:""`
}) (CreateSnapshotResult, error) {
	req := in.Body
	if req.Dataset == "" || req.SnapName == "" {
		return CreateSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset and snapname are required"))
	}
	if !validZFSName(req.Dataset) {
		return CreateSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if !validSnapLabel(req.SnapName) {
		return CreateSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid snapshot label"))
	}
	if ok, err := datasetExists(req.Dataset); err != nil {
		return CreateSnapshotResult{}, err
	} else if !ok {
		return CreateSnapshotResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", req.Dataset))
	}

	argv := []string{"zfs", "snapshot"}
	if req.Recursive {
		argv = append(argv, "-r")
	}
	argv = append(argv, req.Dataset+"@"+req.SnapName)
	out, err := h.runLocal(ctx, ops.Step{Name: "Create snapshot " + req.Dataset + "@" + req.SnapName, Argv: argv})
	auditLog(ctx, "snapshot.create", req.Dataset+"@"+req.SnapName, err)
	if err != nil {
		return CreateSnapshotResult{}, localError(err, out)
	}

	h.publishSnapshots()
	return CreateSnapshotResult{
		Snapshot: req.Dataset + "@" + req.SnapName,
		Created:  time.Now().UTC().Format(time.RFC3339),
		Tasks:    out.Steps(),
	}, nil
}

// CreateSnapshotRequest is the POST /api/snapshots body.
type CreateSnapshotRequest struct {
	Dataset   string `json:"dataset"`
	SnapName  string `json:"snapname" doc:"Snapshot label (the part after '@')"`
	Recursive bool   `json:"recursive,omitempty" doc:"Also snapshot all descendant datasets"`
}

// CreateSnapshotResult is the POST /api/snapshots response.
type CreateSnapshotResult struct {
	Snapshot string             `json:"snapshot"`
	Created  string             `json:"created" doc:"RFC 3339 UTC timestamp"`
	Tasks    []ansible.TaskStep `json:"tasks"`
}

// RenameDatasetRequest is the POST /api/datasets/rename body.
type RenameDatasetRequest struct {
	Name    string `json:"name"`
	NewName string `json:"new_name" doc:"New name; must share the parent dataset"`
}

// RenameDatasetResult is the POST /api/datasets/rename response.
type RenameDatasetResult struct {
	Name    string             `json:"name"`
	NewName string             `json:"new_name"`
	Tasks   []ansible.TaskStep `json:"tasks"`
}

// renameDataset handles POST /api/datasets/rename
// Body: {"name":"tank/old", "new_name":"tank/new"}
func (h *Handler) renameDataset(ctx context.Context, in struct {
	Body RenameDatasetRequest `body:""`
}) (RenameDatasetResult, error) {
	req := in.Body
	if req.Name == "" || req.NewName == "" {
		return RenameDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("name and new_name are required"))
	}
	if !validZFSName(req.Name) || !validZFSName(req.NewName) {
		return RenameDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if !strings.Contains(req.Name, "/") || !strings.Contains(req.NewName, "/") {
		return RenameDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("refusing to rename a pool root"))
	}
	// Both names must share the same parent (ZFS rename constraint).
	oldParent := req.Name[:strings.LastIndex(req.Name, "/")]
	newParent := req.NewName[:strings.LastIndex(req.NewName, "/")]
	if oldParent != newParent {
		return RenameDatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("new name must be under the same parent dataset"))
	}
	h.refMu.Lock()
	defer h.refMu.Unlock()
	if err := h.checkDatasetRefs(refRename, req.Name, true, ""); err != nil {
		return RenameDatasetResult{}, err
	}

	out, err := h.runOp(ctx, "zfs_dataset_rename.yml", map[string]string{
		"name":     req.Name,
		"new_name": req.NewName,
	})
	auditLog(ctx, "dataset.rename", req.Name+" -> "+req.NewName, err)
	if err != nil {
		return RenameDatasetResult{}, playbookError(err, out)
	}

	h.publishDatasets()
	h.publishSnapshots()
	return RenameDatasetResult{Name: req.Name, NewName: req.NewName, Tasks: out.Steps()}, nil
}

// deleteDataset handles DELETE /api/datasets/{name}
// Pool roots (names without a '/') are rejected — use `zpool destroy` for that.
// Names containing '@' are rejected — use DELETE /api/snapshots instead.
// Pass ?recursive=true to also destroy all child datasets and snapshots.
func (h *Handler) deleteDataset(ctx context.Context, in deleteDatasetParams) (DatasetResult, error) {
	name := in.Name
	if name == "" {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(name) {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if !strings.Contains(name, "/") {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("refusing to destroy a pool root"))
	}
	recursive := "false"
	if in.Recursive == "true" {
		recursive = "true"
	}
	h.refMu.Lock()
	defer h.refMu.Unlock()
	if err := h.checkDatasetRefs(refDestroy, name, recursive == "true", ""); err != nil {
		return DatasetResult{}, err
	}

	out, err := h.runOp(ctx, "zfs_dataset_destroy.yml", map[string]string{
		"name":      name,
		"recursive": recursive,
	})
	auditLog(ctx, "dataset.destroy", name, err)
	if err != nil {
		return DatasetResult{}, playbookError(err, out)
	}

	h.publishDatasets()
	return DatasetResult{Name: name, Tasks: out.Steps()}, nil
}

// CloneSnapshotRequest is the POST /api/snapshots/clone body.
type CloneSnapshotRequest struct {
	Snapshot string `json:"snapshot" doc:"Snapshot to clone, e.g. tank/data@snap1"`
	Target   string `json:"target" doc:"New dataset, e.g. tank/data-clone"`
}

// CloneSnapshotResult is the POST /api/snapshots/clone response.
type CloneSnapshotResult struct {
	Snapshot string             `json:"snapshot"`
	Target   string             `json:"target"`
	Tasks    []ansible.TaskStep `json:"tasks"`
}

// cloneSnapshot handles POST /api/snapshots/clone
// Body: {"snapshot":"tank/data@snap1", "target":"tank/data-clone"}
func (h *Handler) cloneSnapshot(ctx context.Context, in struct {
	Body CloneSnapshotRequest `body:""`
}) (CloneSnapshotResult, error) {
	req := in.Body
	if req.Snapshot == "" || req.Target == "" {
		return CloneSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("snapshot and target are required"))
	}
	// Validate snapshot: must contain '@', dataset part must be valid.
	at := strings.IndexByte(req.Snapshot, '@')
	if at < 0 {
		return CloneSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("snapshot must contain '@'"))
	}
	dsName := req.Snapshot[:at]
	label := req.Snapshot[at+1:]
	if !validZFSName(dsName) || !validSnapLabel(label) {
		return CloneSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid snapshot name"))
	}
	if !validZFSName(req.Target) {
		return CloneSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid target dataset name"))
	}
	if !strings.Contains(req.Target, "/") {
		return CloneSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("target must include a pool prefix"))
	}

	out, err := h.runLocal(ctx, ops.Step{
		Name: "Clone " + req.Snapshot + " to " + req.Target,
		Argv: []string{"zfs", "clone", req.Snapshot, req.Target},
	})
	auditLog(ctx, "snapshot.clone", req.Snapshot+" -> "+req.Target, err)
	if err != nil {
		return CloneSnapshotResult{}, localError(err, out)
	}

	h.publishDatasets()
	h.publishSnapshots()
	return CloneSnapshotResult{Snapshot: req.Snapshot, Target: req.Target, Tasks: out.Steps()}, nil
}

// SendSnapshotRequest is the POST /api/snapshots/send body.
type SendSnapshotRequest struct {
	Snapshot        string `json:"snapshot" doc:"Snapshot to send, e.g. tank/data@snap1"`
	Target          string `json:"target" doc:"Receiving dataset, e.g. backup/data"`
	IncrementalFrom string `json:"incremental_from,omitempty" doc:"Earlier snapshot for an incremental send"`
	Raw             bool   `json:"raw,omitempty" doc:"zfs send --raw (keeps encrypted data encrypted)"`
	Remote          string `json:"remote,omitempty" doc:"user@host to receive over SSH; omit for a local receive"`
}

// SendSnapshotResult is the 202 response of POST /api/snapshots/send.
type SendSnapshotResult struct {
	JobID     string    `json:"job_id"`
	Type      string    `json:"type"`
	StartedAt time.Time `json:"started_at"`
	Snapshot  string    `json:"snapshot"`
	Target    string    `json:"target" doc:"Receiving dataset, prefixed with remote: for SSH sends"`
}

// sendSnapshot handles POST /api/snapshots/send
// Body: {"snapshot":"tank/data@snap1", "target":"backup/data",
//
//	"incremental_from":"tank/data@prev", "raw":false, "remote":"user@host"}
//
// Dispatches the transfer as a background job and returns 202 with the job
// metadata. Progress and result are tracked in the jobs panel; clients should
// subscribe to the `jobs.update` SSE topic for live updates. `remote` is
// optional; when set, the receive runs over SSH and the operator must have
// keys pre-configured for the dumpstore service account on `host`.
//
// This is a deliberate exception to the "writes go through Ansible" rule:
// `zfs send | zfs recv` is a streaming data-plane operation, not an
// idempotent configuration write, and may run for hours.
func (h *Handler) sendSnapshot(ctx context.Context, in struct {
	Body SendSnapshotRequest `body:""`
}) (SendSnapshotResult, error) {
	req := in.Body
	if req.Snapshot == "" || req.Target == "" {
		return SendSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("snapshot and target are required"))
	}
	at := strings.IndexByte(req.Snapshot, '@')
	if at < 0 {
		return SendSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("snapshot must contain '@'"))
	}
	if !validZFSName(req.Snapshot[:at]) || !validSnapLabel(req.Snapshot[at+1:]) {
		return SendSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid snapshot name"))
	}
	if !validZFSName(req.Target) || !strings.Contains(req.Target, "/") {
		return SendSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("target must be a dataset path with a pool prefix"))
	}
	if req.IncrementalFrom != "" {
		iat := strings.IndexByte(req.IncrementalFrom, '@')
		if iat < 0 || !validZFSName(req.IncrementalFrom[:iat]) || !validSnapLabel(req.IncrementalFrom[iat+1:]) {
			return SendSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid incremental_from snapshot"))
		}
	}
	if req.Remote != "" && !validRemoteSpec(req.Remote) {
		return SendSnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid remote (expected user@host)"))
	}
	if ok, err := snapshotExists(req.Snapshot); err != nil {
		return SendSnapshotResult{}, err
	} else if !ok {
		return SendSnapshotResult{}, errStatus(http.StatusNotFound, fmt.Errorf("snapshot %q not found", req.Snapshot))
	}

	dest := req.Target
	if req.Remote != "" {
		dest = req.Remote + ":" + req.Target
	}

	// Build argv directly — no shell involved, so no bash dependency on
	// FreeBSD and no portability issues with `pipefail`. Pipefail-equivalent
	// semantics are implemented by jobs.RunPipeline (see Manager.RunPipeline).
	send := []string{"zfs", "send"}
	if req.Raw {
		send = append(send, "--raw")
	}
	if req.IncrementalFrom != "" {
		send = append(send, "-i", req.IncrementalFrom)
	}
	send = append(send, req.Snapshot)

	var recv []string
	if req.Remote != "" {
		recv = []string{"ssh", "-o", "BatchMode=yes", req.Remote, "zfs", "recv", req.Target}
	} else {
		recv = []string{"zfs", "recv", req.Target}
	}

	job, err := h.jobs.RunPipeline(ctx, "snapshot.send", send, recv)
	auditLog(ctx, "snapshot.send", req.Snapshot+" -> "+dest, err)
	if err != nil {
		return SendSnapshotResult{}, fmt.Errorf("failed to start job: %w", err)
	}

	return SendSnapshotResult{
		JobID:     job.ID,
		Type:      job.Type,
		StartedAt: job.StartedAt,
		Snapshot:  req.Snapshot,
		Target:    dest,
	}, nil
}

// UserSpaceResult is the GET /api/userspace/{name...} response.
type UserSpaceResult struct {
	Dataset string         `json:"dataset"`
	Kind    string         `json:"kind" enum:"user,group"`
	Rows    []zfs.SpaceRow `json:"rows"`
}

// getUserSpace handles GET /api/userspace/{name...}?kind=user|group
// Returns per-user or per-group space consumption and quotas for a dataset
// via `zfs userspace` / `zfs groupspace`.
func (h *Handler) getUserSpace(_ context.Context, in userSpaceParams) (UserSpaceResult, error) {
	name := in.Name
	if name == "" || !validZFSName(name) {
		return UserSpaceResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	kind := in.Kind
	if kind == "" {
		kind = "user"
	}
	if kind != "user" && kind != "group" {
		return UserSpaceResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("kind must be user or group"))
	}
	if ok, err := datasetExists(name); err != nil {
		return UserSpaceResult{}, err
	} else if !ok {
		return UserSpaceResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}
	var rows []zfs.SpaceRow
	var err error
	if kind == "group" {
		rows, err = zfs.GroupSpace(name)
	} else {
		rows, err = zfs.UserSpace(name)
	}
	if err != nil {
		return UserSpaceResult{}, err
	}
	return UserSpaceResult{Dataset: name, Kind: kind, Rows: rows}, nil
}

// SetUserQuotaRequest is the POST /api/userquota/{name...} body.
type SetUserQuotaRequest struct {
	Kind      string `json:"kind" enum:"user,group"`
	Principal string `json:"principal" doc:"User or group name"`
	Quota     string `json:"quota" doc:"Size like 10G, or 'none' to clear"`
}

// setUserQuota handles POST /api/userquota/{name...}
// Body: { "kind": "user"|"group", "principal": "alice", "quota": "10G"|"none" }
// Sets userquota@<principal> / groupquota@<principal> on the dataset.
func (h *Handler) setUserQuota(ctx context.Context, in struct {
	datasetNameParam
	Body SetUserQuotaRequest `body:""`
}) (DatasetTaskResult, error) {
	name := in.Name
	if name == "" || !validZFSName(name) {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	req := in.Body
	if req.Kind != "user" && req.Kind != "group" {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("kind must be user or group"))
	}
	if !validUnixName(req.Principal) {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid %s name", req.Kind))
	}
	if !validZFSSize(req.Quota) {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid quota (size like 10G, or 'none')"))
	}
	if ok, err := datasetExists(name); err != nil {
		return DatasetTaskResult{}, err
	} else if !ok {
		return DatasetTaskResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}
	out, err := h.runLocal(ctx, ops.Step{
		Name: "Set " + req.Kind + "quota@" + req.Principal + " on " + name,
		Argv: []string{"zfs", "set", req.Kind + "quota@" + req.Principal + "=" + req.Quota, name},
	})
	auditLog(ctx, "dataset.quota_set", name+" "+req.Kind+"quota@"+req.Principal+"="+req.Quota, err)
	if err != nil {
		return DatasetTaskResult{}, localError(err, out)
	}
	return DatasetTaskResult{Dataset: name, Tasks: out.Steps()}, nil
}

// maxDiffEntries caps the number of diff entries returned to the client so a
// huge diff on a busy dataset cannot produce an unbounded response.
const maxDiffEntries = 10000

// getSnapshotDiff handles GET /api/snapshots/diff?from=<snap>&to=<snap|empty>
// Runs `zfs diff` between two snapshots of a dataset, or between a snapshot
// and the live filesystem when to is omitted.
func (h *Handler) getSnapshotDiff(_ context.Context, in snapshotDiffParams) (SnapshotDiffResult, error) {
	from, to := in.From, in.To

	at := strings.IndexByte(from, '@')
	if at < 0 || !validZFSName(from[:at]) || !validSnapLabel(from[at+1:]) {
		return SnapshotDiffResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("from must be a valid snapshot name (dataset@label)"))
	}
	dataset := from[:at]
	if to != "" {
		tat := strings.IndexByte(to, '@')
		if tat < 0 || !validSnapLabel(to[tat+1:]) {
			return SnapshotDiffResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("to must be a valid snapshot name (dataset@label)"))
		}
		if to[:tat] != dataset {
			return SnapshotDiffResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("to must be a snapshot of the same dataset (%s)", dataset))
		}
	}
	if ok, err := snapshotExists(from); err != nil {
		return SnapshotDiffResult{}, err
	} else if !ok {
		return SnapshotDiffResult{}, errStatus(http.StatusNotFound, fmt.Errorf("snapshot %q not found", from))
	}

	entries, err := zfs.DiffSnapshots(from, to)
	if err != nil {
		return SnapshotDiffResult{}, fmt.Errorf("zfs diff: %w", err)
	}
	truncated := false
	if len(entries) > maxDiffEntries {
		entries = entries[:maxDiffEntries]
		truncated = true
	}
	return SnapshotDiffResult{From: from, To: to, Entries: entries, Truncated: truncated}, nil
}

// SnapshotDiffResult is the GET /api/snapshots/diff response.
type SnapshotDiffResult struct {
	From      string          `json:"from"`
	To        string          `json:"to" doc:"Empty when diffing against the live filesystem"`
	Entries   []zfs.DiffEntry `json:"entries"`
	Truncated bool            `json:"truncated" doc:"True when the diff was cut at 10000 entries"`
}

// RewriteRequest is the POST /api/rewrite/{name...} body.
type RewriteRequest struct {
	Recursive          bool `json:"recursive,omitempty" doc:"zfs rewrite -r"`
	SkipSnapshotShared bool `json:"skip_snapshot_shared,omitempty" doc:"zfs rewrite -S: skip blocks shared with snapshots"`
	SkipCloneShared    bool `json:"skip_clone_shared,omitempty" doc:"zfs rewrite -C: skip blocks shared with clones"`
}

// RewriteResult is the 202 response of POST /api/rewrite/{name...}.
type RewriteResult struct {
	JobID      string    `json:"job_id"`
	Type       string    `json:"type"`
	StartedAt  time.Time `json:"started_at"`
	Dataset    string    `json:"dataset"`
	Mountpoint string    `json:"mountpoint"`
}

// rewriteDataset handles POST /api/rewrite/{name...}
// Body: { "recursive": bool, "skip_snapshot_shared": bool, "skip_clone_shared": bool }
// Dispatches `zfs rewrite` on the dataset's mountpoint as a background job —
// rewriting every block of a large dataset can run for hours, so it goes
// through the jobs manager rather than Ansible.
func (h *Handler) rewriteDataset(ctx context.Context, in struct {
	datasetNameParam
	Body RewriteRequest `body:""`
}) (RewriteResult, error) {
	name := in.Name
	if name == "" || !validZFSName(name) {
		return RewriteResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	req := in.Body

	datasets, err := zfs.ListDatasets()
	if err != nil {
		return RewriteResult{}, err
	}
	var ds *zfs.Dataset
	for i := range datasets {
		if datasets[i].Name == name {
			ds = &datasets[i]
			break
		}
	}
	if ds == nil {
		return RewriteResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}
	// `zfs rewrite` operates on files through the mounted filesystem.
	if ds.Type != "filesystem" {
		return RewriteResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("only filesystems can be rewritten (got %s)", ds.Type))
	}
	if !validShellPath(ds.Mountpoint) {
		return RewriteResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset has no usable mountpoint (%q) — it must be mounted", ds.Mountpoint))
	}

	argv := []string{"zfs", "rewrite"}
	if req.Recursive {
		argv = append(argv, "-r")
	}
	if req.SkipSnapshotShared {
		argv = append(argv, "-S")
	}
	if req.SkipCloneShared {
		argv = append(argv, "-C")
	}
	argv = append(argv, ds.Mountpoint)

	job, err := h.jobs.Run(ctx, "dataset.rewrite", argv)
	auditLog(ctx, "dataset.rewrite", name, err)
	if err != nil {
		return RewriteResult{}, fmt.Errorf("failed to start job: %w", err)
	}
	return RewriteResult{
		JobID:      job.ID,
		Type:       job.Type,
		StartedAt:  job.StartedAt,
		Dataset:    name,
		Mountpoint: ds.Mountpoint,
	}, nil
}

// SnapshotResult is the result of a write on one snapshot.
type SnapshotResult struct {
	Snapshot string             `json:"snapshot"`
	Tasks    []ansible.TaskStep `json:"tasks"`
}

// deleteSnapshot handles DELETE /api/snapshots/{dataset@snapname}
func (h *Handler) deleteSnapshot(ctx context.Context, in deleteSnapshotParams) (SnapshotResult, error) {
	snapshot := in.Snapshot
	if snapshot == "" {
		return SnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("snapshot path required"))
	}
	parts := strings.SplitN(snapshot, "@", 2)
	if len(parts) != 2 || !validZFSName(parts[0]) || !validSnapLabel(parts[1]) {
		return SnapshotResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid snapshot name (expected dataset@label)"))
	}
	if ok, err := snapshotExists(snapshot); err != nil {
		return SnapshotResult{}, err
	} else if !ok {
		return SnapshotResult{}, errStatus(http.StatusNotFound, fmt.Errorf("snapshot %q not found", snapshot))
	}

	argv := []string{"zfs", "destroy"}
	if in.Recursive == "true" {
		argv = append(argv, "-r")
	}
	argv = append(argv, snapshot)
	out, err := h.runLocal(ctx, ops.Step{Name: "Destroy snapshot " + snapshot, Argv: argv})
	auditLog(ctx, "snapshot.destroy", snapshot, err)
	if err != nil {
		return SnapshotResult{}, localError(err, out)
	}

	h.publishSnapshots()
	return SnapshotResult{Snapshot: snapshot, Tasks: out.Steps()}, nil
}

// DeleteSnapshotsRequest is the POST /api/snapshots/delete-batch body.
type DeleteSnapshotsRequest struct {
	Snapshots []string `json:"snapshots" doc:"1–100 snapshot names (dataset@label)"`
}

// DeleteSnapshotsResult is the POST /api/snapshots/delete-batch response.
type DeleteSnapshotsResult struct {
	Snapshots []string           `json:"snapshots"`
	Tasks     []ansible.TaskStep `json:"tasks"`
}

// deleteSnapshotBatch handles POST /api/snapshots/delete-batch
// Body: {"snapshots": ["tank/data@snap1", "tank/home@snap2"]}
func (h *Handler) deleteSnapshotBatch(ctx context.Context, in struct {
	Body DeleteSnapshotsRequest `body:""`
}) (DeleteSnapshotsResult, error) {
	body := in.Body
	if len(body.Snapshots) == 0 {
		return DeleteSnapshotsResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("snapshots list is empty"))
	}
	if len(body.Snapshots) > 100 {
		return DeleteSnapshotsResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("too many snapshots (max 100)"))
	}
	for _, snap := range body.Snapshots {
		parts := strings.SplitN(snap, "@", 2)
		if len(parts) != 2 || !validZFSName(parts[0]) || !validSnapLabel(parts[1]) {
			return DeleteSnapshotsResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid snapshot name %q (expected dataset@label)", snap))
		}
	}

	// Pre-release any dumpstore-repl holds we placed during replication runs.
	// Without this, `zfs destroy` fails with "dataset is busy" on snapshots
	// the user is trying to delete from the UI.
	if h.repl != nil {
		h.repl.ReleaseHoldsFor(body.Snapshots)
	}

	// One step per snapshot; keep destroying past individual failures and
	// report the aggregate, matching the old batch playbook semantics.
	steps := make([]ops.Step, 0, len(body.Snapshots))
	for _, snap := range body.Snapshots {
		steps = append(steps, ops.Step{
			Name:            "Destroy snapshot " + snap,
			Argv:            []string{"zfs", "destroy", snap},
			ContinueOnError: true,
		})
	}
	out, err := h.runLocal(ctx, steps...)
	auditLog(ctx, "snapshot.batch_destroy", strings.Join(body.Snapshots, ","), err)
	if err != nil {
		return DeleteSnapshotsResult{}, localError(err, out)
	}
	h.publishSnapshots()
	return DeleteSnapshotsResult{Snapshots: body.Snapshots, Tasks: out.Steps()}, nil
}

// DatasetOwnership is the GET /api/chown/{dataset...} response.
type DatasetOwnership struct {
	Dataset    string `json:"dataset"`
	Mountpoint string `json:"mountpoint"`
	Owner      string `json:"owner"`
	Group      string `json:"group"`
}

// getDatasetOwnership handles GET /api/chown/{dataset...}
// Returns the current owner and group of the dataset's mountpoint directory.
func (h *Handler) getDatasetOwnership(_ context.Context, in datasetParam) (DatasetOwnership, error) {
	name := in.Dataset
	if name == "" {
		return DatasetOwnership{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(name) {
		return DatasetOwnership{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	props, err := zfs.GetDatasetProps(name)
	if err != nil {
		return DatasetOwnership{}, err
	}
	mp := props["mountpoint"].Value
	if mp == "none" || mp == "-" || mp == "" {
		return DatasetOwnership{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name))
	}
	owner, group, err := zfs.GetMountpointOwnership(mp)
	if err != nil {
		return DatasetOwnership{}, err
	}
	return DatasetOwnership{Dataset: name, Mountpoint: mp, Owner: owner, Group: group}, nil
}

// SetOwnershipRequest is the POST /api/chown/{dataset...} body.
type SetOwnershipRequest struct {
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Recursive bool   `json:"recursive,omitempty" doc:"chown -R"`
}

// setDatasetOwnership handles POST /api/chown/{dataset...}
// Body: {"owner":"alice","group":"storage","recursive":false}
func (h *Handler) setDatasetOwnership(ctx context.Context, in struct {
	datasetParam
	Body SetOwnershipRequest `body:""`
}) (DatasetTaskResult, error) {
	name := in.Dataset
	if name == "" {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(name) {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if ok, err := datasetExists(name); err != nil {
		return DatasetTaskResult{}, err
	} else if !ok {
		return DatasetTaskResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}

	req := in.Body
	if req.Owner == "" || req.Group == "" {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("owner and group are required"))
	}
	if !validUnixName(req.Owner) || !validUnixName(req.Group) {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid owner or group name"))
	}

	props, err := zfs.GetDatasetProps(name)
	if err != nil {
		return DatasetTaskResult{}, err
	}
	mp := props["mountpoint"].Value
	if mp == "none" || mp == "-" || mp == "" {
		return DatasetTaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name))
	}

	recursive := "false"
	if req.Recursive {
		recursive = "true"
	}

	out, err := h.runOp(ctx, "dataset_chown.yml", map[string]string{
		"mountpoint": mp,
		"owner":      req.Owner,
		"group":      req.Group,
		"recursive":  recursive,
	})
	auditLog(ctx, "dataset.chown", name, err)
	if err != nil {
		return DatasetTaskResult{}, playbookError(err, out)
	}
	return DatasetTaskResult{Dataset: name, Tasks: out.Steps()}, nil
}

// publishDatasets re-reads the dataset list and immediately pushes
// dataset.query to all SSE subscribers. Called after any dataset mutation.
func (h *Handler) publishDatasets() {
	if datasets, err := zfs.ListDatasets(); err == nil {
		h.broker.Publish("dataset.query", datasets)
	}
}

// publishSnapshots re-reads all snapshots and pushes snapshot.query to SSE subscribers.
func (h *Handler) publishSnapshots() {
	if snaps, err := zfs.ListSnapshots(); err == nil {
		h.broker.Publish("snapshot.query", snaps)
	}
}

// publishPools re-reads pool data and pushes pool.query and poolstatus to SSE subscribers.
func (h *Handler) publishPools() {
	if pools, err := zfs.ListPools(); err == nil {
		h.broker.Publish("pool.query", pools)
	}
	if statuses, err := zfs.PoolStatuses(); err == nil {
		h.broker.Publish("poolstatus", statuses)
	}
}

// startScrub handles POST /api/scrub/{pool}
// Initiates a scrub on the named pool via `zpool scrub`.
func (h *Handler) startScrub(ctx context.Context, in poolParam) (PoolResult, error) {
	if in.Pool == "" {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("pool name required"))
	}
	if !validZFSName(in.Pool) || strings.Contains(in.Pool, "/") {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	out, err := h.runLocal(ctx, ops.Step{Name: "Start scrub on " + in.Pool, Argv: []string{"zpool", "scrub", in.Pool}})
	auditLog(ctx, "scrub.start", in.Pool, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	return PoolResult{Pool: in.Pool, Tasks: out.Steps()}, nil
}

// cancelScrub handles DELETE /api/scrub/{pool}
// Cancels a running scrub on the named pool via `zpool scrub -s`.
func (h *Handler) cancelScrub(ctx context.Context, in poolParam) (PoolResult, error) {
	if in.Pool == "" {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("pool name required"))
	}
	if !validZFSName(in.Pool) || strings.Contains(in.Pool, "/") {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}
	out, err := h.runLocal(ctx, ops.Step{Name: "Cancel scrub on " + in.Pool, Argv: []string{"zpool", "scrub", "-s", in.Pool}})
	auditLog(ctx, "scrub.cancel", in.Pool, err)
	if err != nil {
		return PoolResult{}, localError(err, out)
	}
	h.publishPools()
	return PoolResult{Pool: in.Pool, Tasks: out.Steps()}, nil
}

// listScrubSchedules handles GET /api/scrub-schedules
// Returns mode + schedules from the platform cron source (Linux) or
// /etc/periodic.conf (FreeBSD).
func (h *Handler) listScrubSchedules(context.Context, struct{}) (zfs.ScrubScheduleList, error) {
	list, err := zfs.ScrubSchedules()
	if err != nil {
		return list, fmt.Errorf("reading scrub schedules: %w", err)
	}
	return list, nil
}

// setScrubSchedule handles PUT /api/scrub-schedule/{pool}
// Linux:   adds pool to ZFS_SCRUB_POOLS in /etc/default/zfs.
// FreeBSD: adds pool to daily_scrub_zfs_pools in /etc/periodic.conf and sets
//
//	the scrub threshold (threshold_days, default 35).
func (h *Handler) setScrubSchedule(ctx context.Context, in struct {
	poolParam
	Body ScrubScheduleRequest `body:"optional"`
}) (PoolResult, error) {
	pool := in.Pool
	if pool == "" || !validZFSName(pool) || strings.Contains(pool, "/") {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}

	if zfs.OSType() == "freebsd" {
		days := in.Body.ThresholdDays
		if days <= 0 {
			days = 35
		}
		out, err := h.runOp(ctx, "zfs_scrub_periodic_enable.yml", map[string]string{
			"pool":           pool,
			"threshold_days": strconv.Itoa(days),
		})
		auditLog(ctx, "scrub_schedule.set", pool, err)
		if err != nil {
			return PoolResult{}, playbookError(err, out)
		}
		return PoolResult{Pool: pool, Tasks: out.Steps()}, nil
	}

	// Linux: add to ZFS_SCRUB_POOLS — no schedule params required.
	out, err := h.runOp(ctx, "zfs_scrub_zfsutils_enable.yml", map[string]string{"pool": pool})
	auditLog(ctx, "scrub_schedule.set", pool, err)
	if err != nil {
		return PoolResult{}, playbookError(err, out)
	}
	return PoolResult{Pool: pool, Tasks: out.Steps()}, nil
}

// ScrubScheduleRequest is the optional PUT /api/scrub-schedule/{pool} body.
type ScrubScheduleRequest struct {
	ThresholdDays int `json:"threshold_days,omitempty" doc:"FreeBSD periodic(8): scrub when the last scrub is older than this many days (default 35). Ignored on Linux."`
}

// deleteScrubSchedule handles DELETE /api/scrub-schedule/{pool}
// Linux:   removes pool from ZFS_SCRUB_POOLS in /etc/default/zfs.
// FreeBSD: removes pool from daily_scrub_zfs_pools in /etc/periodic.conf.
func (h *Handler) deleteScrubSchedule(ctx context.Context, in poolParam) (PoolResult, error) {
	pool := in.Pool
	if pool == "" || !validZFSName(pool) || strings.Contains(pool, "/") {
		return PoolResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid pool name"))
	}

	playbook := "zfs_scrub_zfsutils_disable.yml"
	if zfs.OSType() == "freebsd" {
		playbook = "zfs_scrub_periodic_disable.yml"
	}

	out, err := h.runOp(ctx, playbook, map[string]string{"pool": pool})
	auditLog(ctx, "scrub_schedule.delete", pool, err)
	if err != nil {
		return PoolResult{}, playbookError(err, out)
	}
	return PoolResult{Pool: pool, Tasks: out.Steps()}, nil
}

// listAutoSnapshotSchedules handles GET /api/auto-snapshot-schedules
func (h *Handler) listAutoSnapshotSchedules(context.Context, struct{}) (map[string]zfs.AutoSnapshotProps, error) {
	return zfs.ListAutoSnapshotProps()
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
func (h *Handler) getAutoSnapshotProps(_ context.Context, p datasetNameParam) (zfs.AutoSnapshotProps, error) {
	if !validZFSName(p.Name) {
		return zfs.AutoSnapshotProps{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	return zfs.GetAutoSnapshotProps(p.Name)
}

// setAutoSnapshotProps handles PUT /api/auto-snapshot/{name...}
func (h *Handler) setAutoSnapshotProps(ctx context.Context, in struct {
	datasetNameParam
	Body map[string]string `body:""`
}) (DatasetResult, error) {
	name := in.Name
	if !validZFSName(name) {
		return DatasetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if ok, err := datasetExists(name); err != nil {
		return DatasetResult{}, err
	} else if !ok {
		return DatasetResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}

	vars := map[string]string{"name": name}
	for _, ap := range autoSnapProps {
		val, ok := in.Body[ap.prop]
		if !ok {
			continue // not provided — playbook default (__skip__) applies
		}
		if !validAutoSnapValue(ap.prop, val) {
			return DatasetResult{}, errStatus(http.StatusBadRequest,
				fmt.Errorf("invalid value %q for property %s", val, ap.prop))
		}
		vars[ap.varName] = val
	}

	out, err := h.runOp(ctx, "zfs_autosnap_set.yml", vars)
	auditLog(ctx, "auto_snapshot.set", name, err)
	if err != nil {
		return DatasetResult{}, playbookError(err, out)
	}
	h.publishDatasets()
	h.publishAutoSnapshot()
	return DatasetResult{Name: name, Tasks: out.Steps()}, nil
}
