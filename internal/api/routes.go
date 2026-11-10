package api

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"

	"dumpstore/internal/ansible"
	"dumpstore/internal/apispec"
	"dumpstore/internal/blockdev"
	"dumpstore/internal/broker"
	"dumpstore/internal/iscsi"
	"dumpstore/internal/operations"
	"dumpstore/internal/replication"
	"dumpstore/internal/smart"
	"dumpstore/internal/smb"
)

// Path and query parameter sets shared by several routes. Names containing
// '/' or '@' must be percent-encoded by clients.
type (
	datasetNameParam struct {
		Name string `path:"name" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
	}
	datasetParam struct {
		Dataset string `path:"dataset" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
	}
	idParam struct {
		ID string `path:"id"`
	}
	poolParam struct {
		Pool string `path:"pool" doc:"Pool name"`
	}
	poolDeviceParam struct {
		Pool   string `path:"pool" doc:"Pool name"`
		Device string `path:"device" doc:"Device path or vdev name (percent-encode '/')"`
	}
	userParam struct {
		Name string `path:"name" doc:"User name"`
	}
	groupParam struct {
		Name string `path:"name" doc:"Group name"`
	}
	smbUserParam struct {
		Name string `path:"name" doc:"User name"`
	}
	shareNameParam struct {
		Sharename string `path:"sharename" doc:"Share name"`
	}
	serviceActionParam struct {
		Service string `path:"service" doc:"Logical service name, as listed by GET /api/services"`
		Action  string `path:"action" enum:"start,stop,restart,enable,disable"`
	}
	deleteDatasetParams struct {
		Name      string `path:"name" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Recursive bool   `query:"recursive" doc:"Also destroy descendant datasets and snapshots"`
	}
	deleteSnapshotParams struct {
		Snapshot  string `path:"snapshot" doc:"Snapshot name, e.g. tank/data@before (percent-encode '/' and '@')"`
		Recursive bool   `query:"recursive" doc:"Also destroy same-named snapshots of descendant datasets"`
	}
	userSpaceParams struct {
		Name string `path:"name" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Kind string `query:"kind" enum:"user,group" doc:"Report per-user (default) or per-group usage"`
	}
	snapshotDiffParams struct {
		From string `query:"from" doc:"Snapshot to diff from, e.g. tank/data@before"`
		To   string `query:"to" doc:"Later snapshot of the same dataset; omit to diff against the live filesystem"`
	}
	removeACLParams struct {
		Dataset   string `path:"dataset" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Entry     string `query:"entry" doc:"ACL entry to remove, in the form returned by GET /api/acl/{dataset}"`
		Recursive bool   `query:"recursive" doc:"Apply to all files below the mountpoint"`
	}
	deleteSMBShareParams struct {
		Dataset string `path:"dataset" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Name    string `query:"name" doc:"Share name"`
	}
	deleteISCSIParams struct {
		IQN  string `query:"iqn" doc:"Target IQN"`
		Zvol string `query:"zvol" doc:"Backing zvol, e.g. tank/vol1"`
	}
	eventsParams struct {
		Topics string `query:"topics" doc:"Comma-separated topic names"`
	}
)

// jsonObject documents a JSON-object request body whose schema is not yet
// typed (anonymous request structs; Phase 2 of #181 names them).
type jsonObject = map[string]any

// errorResponse is the error body: apiError plus the duplicate flag the
// Operations middleware sets on a reused operation ID (#184).
type errorResponse struct {
	apiError
	DuplicateOperation bool `json:"duplicate_operation,omitempty"`
}

// routeOpt sets optional Route metadata.
type routeOpt func(*apispec.Route)

func params[T any]() routeOpt { return func(r *apispec.Route) { r.Params = reflect.TypeFor[T]() } }
func body[T any]() routeOpt   { return func(r *apispec.Route) { r.Request = reflect.TypeFor[T]() } }
func reply[T any]() routeOpt  { return func(r *apispec.Route) { r.Response = reflect.TypeFor[T]() } }
func status(code int) routeOpt {
	return func(r *apispec.Route) { r.Status = code }
}
func describe(d string) routeOpt { return func(r *apispec.Route) { r.Description = d } }

// route builds the metadata for "METHOD /path" with SDK "group.method".
func route(pattern, sdk, summary string, opts ...routeOpt) apispec.Route {
	method, path, _ := strings.Cut(pattern, " ")
	i := strings.LastIndex(sdk, ".")
	r := apispec.Route{Method: method, Path: path, Group: sdk[:i], Name: sdk[i+1:], Summary: summary}
	for _, o := range opts {
		o(&r)
	}
	return r
}

// raw registers a plain handler; its types come from the route options.
func raw(reg *apispec.Registry, r apispec.Route, fn http.HandlerFunc) {
	r.Handler = fn
	reg.Handle(r)
}

// NewRegistry returns a route registry on mux with the API's shared error
// and op-log step types.
func NewRegistry(mux *http.ServeMux) *apispec.Registry {
	reg := apispec.New(mux)
	reg.Error = reflect.TypeFor[errorResponse]()
	reg.TaskStep = reflect.TypeFor[ansible.TaskStep]()
	reg.Tags = map[string]string{
		"auth":         "Login, session and admin credentials",
		"autoSnapshot": "Who runs com.sun:auto-snapshot (OS daemon or dumpstore's scheduler)",
		"datasets":     "Filesystems and volumes: properties, quotas, ownership, ACLs, auto-snapshot settings",
		"devices":      "Physical block devices",
		"events":       "Server-sent events for live UI updates",
		"groups":       "Local Unix groups",
		"iscsi":        "iSCSI targets backed by zvols (targetcli on Linux, ctld on FreeBSD)",
		"jobs":         "Long-running background jobs (send/receive, rewrite, replication runs)",
		"meta":         "This API description",
		"metrics":      "Prometheus metrics",
		"operations":   "Progress and results of write operations",
		"pools":        "Pools, vdevs, devices, scrubs and I/O statistics",
		"replication":  "Scheduled snapshot replication tasks",
		"services":     "Managed system services",
		"smb":          "Samba: shares, users, [homes], Time Machine and wsdd",
		"snapshots":    "Snapshots: create, destroy, clone, send, diff",
		"system":       "Host information, network, SMART and dumpstore version",
		"tls":          "HTTPS certificate and ACME configuration",
		"users":        "Local Unix users and their SSH keys",
	}
	reg.Names = map[reflect.Type]string{
		reflect.TypeFor[errorResponse]():              "Error",
		reflect.TypeFor[smart.Result]():               "SmartReport",
		reflect.TypeFor[operations.Op]():              "Operation",
		reflect.TypeFor[operations.Step]():            "OperationStep",
		reflect.TypeFor[replication.Task]():           "ReplicationTask",
		reflect.TypeFor[replication.RunRecord]():      "ReplicationRun",
		reflect.TypeFor[replicationRequest]():         "ReplicationTaskInput",
		reflect.TypeFor[iscsi.Target]():               "ISCSITarget",
		reflect.TypeFor[blockdev.Device]():            "BlockDevice",
		reflect.TypeFor[tlsStatusResponse]():          "TLSStatus",
		reflect.TypeFor[autoSnapshotStatusResponse](): "AutoSnapshotStatus",
	}
	return reg
}

// RegisterRoutes attaches all API routes to reg, with their OpenAPI
// metadata (#181). Prefer handle (typed) for new routes.
func (h *Handler) RegisterRoutes(reg *apispec.Registry) {
	// System
	handle(reg, route("GET /api/sysinfo", "system.info", "Host, OS, ZFS and software information"), h.getSysInfo)
	handle(reg, route("GET /api/network", "system.network", "Network interfaces and addresses"), h.getNetwork)
	raw(reg, route("GET /api/version", "system.version", "dumpstore version"), h.getVersion)
	handle(reg, route("GET /api/smart", "system.smart", "SMART health of all drives"), h.getSMART)
	raw(reg, route("GET /api/schema", "system.schema", "Dataset property metadata and ZFS capabilities for the UI"), h.getSchema)
	handle(reg, route("GET /api/operations/{id}", "operations.get", "State, steps and result of a write operation",
		describe("Steps are returned after the given seq; the result is kept for finished operations up to 1 h (#184).")), h.getOperation)
	raw(reg, route("GET /api/events", "events.stream", "Server-sent event stream",
		params[eventsParams](),
		func(r *apispec.Route) {
			r.ResponseType = apispec.Event
			r.Internal = true
			r.Description = "Subscribe to one or more topics. Cached topics replay their last value on connect. Topics: " +
				strings.Join(slices.Sorted(maps.Keys(broker.ValidTopics)), ", ") + "."
		}), h.getEvents)
	raw(reg, route("GET /metrics", "metrics.scrape", "Prometheus metrics",
		describe("Prometheus text exposition. Unauthenticated by default (unprotected_paths in dumpstore.conf)."),
		func(r *apispec.Route) { r.ResponseType = apispec.Text; r.Public = true; r.Internal = true }), h.getMetrics)

	// Pools and devices
	handle(reg, route("GET /api/pools", "pools.list", "List pools"), h.getPools)
	handle(reg, route("GET /api/poolstatus", "pools.status", "Detailed status (vdev tree, scan, errors) of all pools"), h.getPoolStatuses)
	handle(reg, route("GET /api/iostat", "pools.iostat", "Per-pool I/O statistics"), h.getIOStat)
	raw(reg, route("POST /api/pools", "pools.create", "Create a pool", body[jsonObject]()), h.createPool)
	handle(reg, route("GET /api/pools/importable", "pools.listImportable", "Pools available for import"), h.getImportablePools)
	raw(reg, route("POST /api/pools/import", "pools.import", "Import a pool", body[jsonObject]()), h.importPool)
	raw(reg, route("POST /api/pools/{pool}/export", "pools.export", "Export a pool", params[poolParam]()), h.exportPool)
	raw(reg, route("POST /api/pools/{pool}/vdevs", "pools.vdevs.add", "Add a data vdev", params[poolParam](), body[jsonObject]()), h.addDataVdev)
	raw(reg, route("POST /api/pools/{pool}/cache", "pools.devices.addCache", "Add cache (L2ARC) devices", params[poolParam](), body[jsonObject]()), h.addCacheDevice)
	raw(reg, route("POST /api/pools/{pool}/log", "pools.devices.addLog", "Add a log (SLOG) vdev", params[poolParam](), body[jsonObject]()), h.addLogDevice)
	raw(reg, route("POST /api/pools/{pool}/spare", "pools.devices.addSpare", "Add hot spares", params[poolParam](), body[jsonObject]()), h.addSpareDevice)
	raw(reg, route("DELETE /api/pools/{pool}/devices/{device...}", "pools.devices.remove", "Remove a device from a pool", params[poolDeviceParam]()), h.removePoolDevice)
	raw(reg, route("POST /api/pools/{pool}/replace", "pools.devices.replace", "Replace a device", params[poolParam](), body[jsonObject]()), h.replaceDevice)
	raw(reg, route("POST /api/pools/{pool}/offline", "pools.devices.offline", "Take a device offline", params[poolParam](), body[jsonObject]()), h.offlineDevice)
	raw(reg, route("POST /api/pools/{pool}/online", "pools.devices.online", "Bring a device online", params[poolParam](), body[jsonObject]()), h.onlineDevice)
	raw(reg, route("POST /api/scrub/{pool}", "pools.scrub.start", "Start a scrub", params[poolParam]()), h.startScrub)
	raw(reg, route("DELETE /api/scrub/{pool}", "pools.scrub.cancel", "Cancel a running scrub", params[poolParam]()), h.cancelScrub)
	handle(reg, route("GET /api/scrub-schedules", "pools.scrub.listSchedules", "Periodic scrub schedules"), h.listScrubSchedules)
	raw(reg, route("PUT /api/scrub-schedule/{pool}", "pools.scrub.setSchedule", "Set a pool's scrub schedule", params[poolParam](), body[jsonObject]()), h.setScrubSchedule)
	raw(reg, route("DELETE /api/scrub-schedule/{pool}", "pools.scrub.removeSchedule", "Remove a pool's scrub schedule", params[poolParam]()), h.deleteScrubSchedule)
	handle(reg, route("GET /api/devices", "devices.list", "Block devices, with pool membership"), h.getDevices)

	// Datasets
	handle(reg, route("GET /api/datasets", "datasets.list", "List datasets and volumes"), h.getDatasets)
	raw(reg, route("POST /api/datasets", "datasets.create", "Create a filesystem or volume", body[jsonObject](), status(http.StatusCreated)), h.createDataset)
	raw(reg, route("PATCH /api/datasets/{name...}", "datasets.update", "Set dataset properties",
		params[datasetNameParam](), body[map[string]string]()), h.setDatasetProps)
	raw(reg, route("DELETE /api/datasets/{name...}", "datasets.delete", "Destroy a dataset or volume",
		params[deleteDatasetParams](), describe("Refused with 409 while shares, iSCSI targets or replication tasks reference it (#183).")), h.deleteDataset)
	handle(reg, route("GET /api/dataset-props/{name...}", "datasets.getProperties", "All properties of a dataset, with source"), h.getDatasetProps)
	raw(reg, route("POST /api/datasets/rename", "datasets.rename", "Rename a dataset", body[jsonObject]()), h.renameDataset)
	raw(reg, route("POST /api/rewrite/{name...}", "datasets.rewrite", "Rewrite dataset data in place (background job)",
		params[datasetNameParam](), body[jsonObject](), status(http.StatusAccepted)), h.rewriteDataset)
	raw(reg, route("GET /api/userspace/{name...}", "datasets.quotas.usage", "Per-user or per-group space usage and quotas", params[userSpaceParams]()), h.getUserSpace)
	raw(reg, route("POST /api/userquota/{name...}", "datasets.quotas.set", "Set or clear a user or group quota", params[datasetNameParam](), body[jsonObject]()), h.setUserQuota)
	raw(reg, route("GET /api/chown/{dataset...}", "datasets.owner.get", "Owner and group of the mountpoint", params[datasetParam]()), h.getDatasetOwnership)
	raw(reg, route("POST /api/chown/{dataset...}", "datasets.owner.set", "Change owner and group of the mountpoint", params[datasetParam](), body[jsonObject]()), h.setDatasetOwnership)
	handle(reg, route("GET /api/acl-status", "datasets.acl.status", "Which datasets carry ACLs"), h.getACLStatus)
	handle(reg, route("GET /api/acl/{dataset...}", "datasets.acl.get", "ACL of a dataset's mountpoint"), h.getDatasetACL)
	raw(reg, route("POST /api/acl/{dataset...}", "datasets.acl.addEntry", "Add or update an ACL entry", params[datasetParam](), body[jsonObject]()), h.setACLEntry)
	raw(reg, route("DELETE /api/acl/{dataset...}", "datasets.acl.removeEntry", "Remove an ACL entry", params[removeACLParams]()), h.removeACLEntry)
	handle(reg, route("GET /api/auto-snapshot/{name...}", "datasets.autoSnapshot.get", "Auto-snapshot properties of a dataset"), h.getAutoSnapshotProps)
	raw(reg, route("PUT /api/auto-snapshot/{name...}", "datasets.autoSnapshot.set", "Set auto-snapshot properties of a dataset",
		params[datasetNameParam](), body[map[string]string]()), h.setAutoSnapshotProps)

	// Snapshots
	handle(reg, route("GET /api/snapshots", "snapshots.list", "List snapshots"), h.getSnapshots)
	raw(reg, route("POST /api/snapshots", "snapshots.create", "Create a snapshot", body[jsonObject](), status(http.StatusCreated)), h.createSnapshot)
	raw(reg, route("DELETE /api/snapshots/{snapshot...}", "snapshots.delete", "Destroy a snapshot", params[deleteSnapshotParams]()), h.deleteSnapshot)
	raw(reg, route("POST /api/snapshots/delete-batch", "snapshots.deleteBatch", "Destroy several snapshots", body[jsonObject]()), h.deleteSnapshotBatch)
	raw(reg, route("POST /api/snapshots/clone", "snapshots.clone", "Clone a snapshot into a new dataset", body[jsonObject](), status(http.StatusCreated)), h.cloneSnapshot)
	raw(reg, route("POST /api/snapshots/send", "snapshots.send", "Send a snapshot to another dataset (background job)", body[jsonObject](), status(http.StatusAccepted)), h.sendSnapshot)
	raw(reg, route("GET /api/snapshots/diff", "snapshots.diff", "Files changed between a snapshot and a later snapshot or the live filesystem", params[snapshotDiffParams]()), h.getSnapshotDiff)

	// Auto-snapshot ownership
	handle(reg, route("GET /api/auto-snapshot-schedules", "autoSnapshot.listSchedules", "Auto-snapshot properties of all datasets"), h.listAutoSnapshotSchedules)
	handle(reg, route("GET /api/auto-snapshot/status", "autoSnapshot.status", "Who runs auto-snapshots (OS daemon or dumpstore) and last runs"), h.getAutoSnapshotStatus)
	raw(reg, route("POST /api/auto-snapshot/takeover", "autoSnapshot.takeover", "Disable the OS daemon and let dumpstore run auto-snapshots"), h.takeoverAutoSnapshot)
	raw(reg, route("POST /api/auto-snapshot/release", "autoSnapshot.release", "Hand auto-snapshots back to the OS daemon"), h.releaseAutoSnapshot)

	// Replication and jobs
	handle(reg, route("GET /api/replication", "replication.list", "List replication tasks"), h.listReplication)
	raw(reg, route("POST /api/replication", "replication.create", "Create a replication task",
		body[replicationRequest](), reply[replication.Task](), status(http.StatusCreated)), h.createReplication)
	raw(reg, route("PATCH /api/replication/{id}", "replication.update", "Update a replication task",
		params[idParam](), body[replicationRequest](), reply[replication.Task]()), h.updateReplication)
	raw(reg, route("DELETE /api/replication/{id}", "replication.delete", "Delete a replication task", params[idParam](), status(http.StatusNoContent)), h.deleteReplication)
	raw(reg, route("POST /api/replication/{id}/run", "replication.run", "Run a replication task now (background job)", params[idParam](), status(http.StatusAccepted)), h.runReplication)
	handle(reg, route("GET /api/replication/{id}/history", "replication.history", "Recent runs of a replication task"), h.getReplicationHistory)
	handle(reg, route("GET /api/jobs", "jobs.list", "List background jobs"), h.listJobs)
	handle(reg, route("GET /api/jobs/{id}", "jobs.get", "Get a background job"), h.getJob)
	raw(reg, route("POST /api/jobs/{id}/cancel", "jobs.cancel", "Cancel a running job", params[idParam](), status(http.StatusNoContent)), h.cancelJob)
	raw(reg, route("DELETE /api/jobs/{id}", "jobs.delete", "Remove a finished job", params[idParam](), status(http.StatusNoContent)), h.removeJob)

	// Users and groups
	handle(reg, route("GET /api/users", "users.list", "List local users"), h.getUsers)
	raw(reg, route("POST /api/users", "users.create", "Create a local user", body[jsonObject](), status(http.StatusCreated)), h.createUser)
	raw(reg, route("PUT /api/users/{name}", "users.update", "Modify a local user", params[userParam](), body[jsonObject]()), h.modifyUser)
	raw(reg, route("DELETE /api/users/{name}", "users.delete", "Delete a local user", params[userParam]()), h.deleteUser)
	raw(reg, route("GET /api/users/{name}/sshkeys", "users.sshKeys.list", "List a user's SSH keys", params[userParam]()), h.listSSHKeys)
	raw(reg, route("POST /api/users/{name}/sshkeys", "users.sshKeys.add", "Add an SSH key", params[userParam](), body[jsonObject]()), h.addSSHKey)
	raw(reg, route("DELETE /api/users/{name}/sshkeys", "users.sshKeys.remove", "Remove an SSH key (key in the body)", params[userParam](), body[jsonObject]()), h.removeSSHKey)
	handle(reg, route("GET /api/groups", "groups.list", "List local groups"), h.getGroups)
	raw(reg, route("POST /api/groups", "groups.create", "Create a local group", body[jsonObject]()), h.createGroup)
	raw(reg, route("PUT /api/groups/{name}", "groups.update", "Rename a group or change its GID or members", params[groupParam](), body[jsonObject]()), h.modifyGroup)
	raw(reg, route("DELETE /api/groups/{name}", "groups.delete", "Delete a local group", params[groupParam]()), h.deleteGroup)

	// SMB
	raw(reg, route("GET /api/smb/status", "smb.status", "Samba initialisation state and wsdd status"), h.getSMBStatus)
	raw(reg, route("POST /api/smb/init", "smb.init", "Initialise Samba (dumpstore takes over smb.conf)"), h.initSamba)
	raw(reg, route("POST /api/smb/wsdd", "smb.wsdd.apply", "Take over the wsdd configuration"), h.applyWSDD)
	raw(reg, route("GET /api/smb-shares", "smb.shares.list", "List SMB usershares", reply[[]smb.Usershare]()), h.getSMBShares)
	raw(reg, route("POST /api/smb-share/{dataset...}", "smb.shares.set", "Share a dataset over SMB", params[datasetParam](), body[jsonObject]()), h.setSMBShare)
	raw(reg, route("DELETE /api/smb-share/{dataset...}", "smb.shares.delete", "Remove an SMB share", params[deleteSMBShareParams]()), h.deleteSMBShare)
	raw(reg, route("GET /api/smb-users", "smb.users.list", "List Samba users"), h.getSambaUsers)
	raw(reg, route("POST /api/smb-users/{name}", "smb.users.add", "Register a user with Samba", params[smbUserParam](), body[jsonObject]()), h.addSambaUser)
	raw(reg, route("DELETE /api/smb-users/{name}", "smb.users.remove", "Remove a user from Samba", params[smbUserParam]()), h.removeSambaUser)
	raw(reg, route("GET /api/smb/homes", "smb.homes.get", "The [homes] share"), h.getSMBHomes)
	raw(reg, route("POST /api/smb/homes", "smb.homes.set", "Enable or update the [homes] share", body[jsonObject]()), h.setSMBHomes)
	raw(reg, route("DELETE /api/smb/homes", "smb.homes.disable", "Disable the [homes] share"), h.deleteSMBHomes)
	handle(reg, route("GET /api/smb/timemachine", "smb.timeMachine.list", "List Time Machine shares"), h.getTimeMachineShares)
	raw(reg, route("POST /api/smb/timemachine", "smb.timeMachine.set", "Create a Time Machine share", body[jsonObject]()), h.createTimeMachineShare)
	raw(reg, route("DELETE /api/smb/timemachine/{sharename}", "smb.timeMachine.delete", "Delete a Time Machine share", params[shareNameParam]()), h.deleteTimeMachineShare)

	// iSCSI
	handle(reg, route("GET /api/iscsi-targets", "iscsi.targets.list", "List iSCSI targets"), h.getISCSITargets)
	raw(reg, route("POST /api/iscsi-targets", "iscsi.targets.create", "Export a zvol as an iSCSI target", body[jsonObject]()), h.createISCSITarget)
	raw(reg, route("DELETE /api/iscsi-targets", "iscsi.targets.delete", "Delete an iSCSI target", params[deleteISCSIParams]()), h.deleteISCSITarget)
	raw(reg, route("GET /api/iscsi/status", "iscsi.status", "iSCSI backend and initialisation state (ctld init gate on FreeBSD)"), h.getISCSIStatus)
	raw(reg, route("POST /api/iscsi/init", "iscsi.init", "Initialise ctld: back up and take over /etc/ctl.conf (FreeBSD)"), h.initISCSI)

	// Services, TLS, auth
	handle(reg, route("GET /api/services", "services.list", "Status of managed services"), h.getServices)
	raw(reg, route("POST /api/services/{service}/{action}", "services.control", "Start, stop, restart, enable or disable a service", params[serviceActionParam]()), h.mutateService)
	handle(reg, route("GET /api/tls/status", "tls.get", "TLS configuration and certificate details"), h.getTLSStatus)
	raw(reg, route("PATCH /api/tls/config", "tls.update", "Update the TLS configuration", body[jsonObject]()), h.tlsSetConfig)
	raw(reg, route("POST /api/tls/gencert", "tls.generateSelfSigned", "Generate a self-signed certificate", body[jsonObject]()), h.tlsGenCert)
	raw(reg, route("POST /api/tls/acme/issue", "tls.acmeIssue", "Issue a certificate via ACME", body[jsonObject]()), h.tlsAcmeIssue)
	raw(reg, route("POST /api/tls/acme/renew", "tls.acmeRenew", "Renew the ACME certificate"), h.tlsAcmeRenew)
	raw(reg, route("POST /api/auth/change-password", "auth.setPassword", "Change the admin password", body[jsonObject]()), h.changePassword)
	raw(reg, route("POST /api/auth/change-username", "auth.setUsername", "Change the admin username", body[jsonObject]()), h.changeUsername)

	// The spec itself. Built once on first request, after main has
	// registered every route (including internal/auth's).
	var (
		specOnce sync.Once
		spec     []byte
		specErr  error
	)
	raw(reg, route("GET /api/v1/openapi.json", "meta.openapi", "This OpenAPI document",
		func(r *apispec.Route) { r.Internal = true; r.Response = reflect.TypeFor[map[string]any]() }),
		func(w http.ResponseWriter, r *http.Request) {
			specOnce.Do(func() { spec, specErr = reg.Spec() })
			if specErr != nil {
				writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("building OpenAPI spec: %w", specErr), nil)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(spec)
		})
}
