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
		Recursive string `query:"recursive" enum:"true,false" doc:"Also destroy descendant datasets and snapshots (only the exact value true enables it)"`
	}
	deleteSnapshotParams struct {
		Snapshot  string `path:"snapshot" doc:"Snapshot name, e.g. tank/data@before (percent-encode '/' and '@')"`
		Recursive string `query:"recursive" enum:"true,false" doc:"Also destroy same-named snapshots of descendant datasets (only the exact value true enables it)"`
	}
	userSpaceParams struct {
		Name string `path:"name" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Kind string `query:"kind" enum:"user,group" doc:"Report per-user (default) or per-group usage"`
	}
	snapshotDiffParams struct {
		From string `query:"from" required:"true" doc:"Snapshot to diff from, e.g. tank/data@before"`
		To   string `query:"to" doc:"Later snapshot of the same dataset; omit to diff against the live filesystem"`
	}
	removeACLParams struct {
		Dataset   string `path:"dataset" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Entry     string `query:"entry" required:"true" doc:"ACL entry to remove, in the form returned by GET /api/acl/{dataset}"`
		Recursive string `query:"recursive" enum:"true,false" doc:"Apply to all files below the mountpoint (only the exact value true enables it)"`
	}
	deleteSMBShareParams struct {
		Dataset string `path:"dataset" doc:"Dataset name, e.g. tank/data (percent-encode '/')"`
		Name    string `query:"name" required:"true" doc:"Share name"`
	}
	deleteISCSIParams struct {
		IQN  string `query:"iqn" required:"true" doc:"Target IQN"`
		Zvol string `query:"zvol" required:"true" doc:"Backing zvol, e.g. tank/vol1"`
	}
	eventsParams struct {
		Topics string `query:"topics" required:"true" doc:"Comma-separated topic names"`
	}
)

// errorResponse is the error body: apiError plus the duplicate flag the
// Operations middleware sets on a reused operation ID (#184).
type errorResponse struct {
	apiError
	DuplicateOperation bool `json:"duplicate_operation,omitempty"`
}

// routeOpt sets optional Route metadata.
type routeOpt func(*apispec.Route)

func params[T any]() routeOpt { return func(r *apispec.Route) { r.Params = reflect.TypeFor[T]() } }
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
	r.Handler = withRemoteAddr(fn)
	reg.Handle(r)
}

// fails documents the 4xx statuses an operation can return (500 is the
// default response everywhere). The integration suite fails on any
// observed status that is not listed.
func fails(codes ...int) routeOpt {
	return func(r *apispec.Route) { r.Errors = append(r.Errors, codes...) }
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
	handle(reg, route("GET /api/version", "system.version", "OpenZFS version"), h.getVersion)
	handle(reg, route("GET /api/smart", "system.smart", "SMART health of all drives"), h.getSMART)
	handle(reg, route("GET /api/schema", "system.schema", "Dataset property metadata and ZFS capabilities for the UI"), h.getSchema)
	handle(reg, route("GET /api/operations/{id}", "operations.get", "State, steps and result of a write operation",
		describe("Steps are returned after the given seq; the result is kept for finished operations up to 1 h (#184)."),
		fails(http.StatusBadRequest, http.StatusNotFound)), h.getOperation)
	raw(reg, route("GET /api/events", "events.stream", "Server-sent event stream",
		params[eventsParams](), fails(http.StatusBadRequest),
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
	handle(reg, route("POST /api/pools", "pools.create", "Create a pool", fails(http.StatusBadRequest, http.StatusConflict)), h.createPool)
	handle(reg, route("GET /api/pools/importable", "pools.listImportable", "Pools available for import"), h.getImportablePools)
	handle(reg, route("POST /api/pools/import", "pools.import", "Import a pool", fails(http.StatusBadRequest)), h.importPool)
	handle(reg, route("POST /api/pools/{pool}/export", "pools.export", "Export a pool", fails(http.StatusBadRequest)), h.exportPool)
	handle(reg, route("POST /api/pools/{pool}/vdevs", "pools.vdevs.add", "Add a data vdev", fails(http.StatusBadRequest)), h.addDataVdev)
	handle(reg, route("POST /api/pools/{pool}/cache", "pools.devices.addCache", "Add cache (L2ARC) devices", fails(http.StatusBadRequest)), h.addCacheDevice)
	handle(reg, route("POST /api/pools/{pool}/log", "pools.devices.addLog", "Add a log (SLOG) vdev", fails(http.StatusBadRequest)), h.addLogDevice)
	handle(reg, route("POST /api/pools/{pool}/spare", "pools.devices.addSpare", "Add hot spares", fails(http.StatusBadRequest)), h.addSpareDevice)
	handle(reg, route("DELETE /api/pools/{pool}/devices/{device...}", "pools.devices.remove", "Remove a device from a pool", fails(http.StatusBadRequest)), h.removePoolDevice)
	handle(reg, route("POST /api/pools/{pool}/replace", "pools.devices.replace", "Replace a device", fails(http.StatusBadRequest)), h.replaceDevice)
	handle(reg, route("POST /api/pools/{pool}/offline", "pools.devices.offline", "Take a device offline", fails(http.StatusBadRequest)), h.offlineDevice)
	handle(reg, route("POST /api/pools/{pool}/online", "pools.devices.online", "Bring a device online", fails(http.StatusBadRequest)), h.onlineDevice)
	handle(reg, route("POST /api/scrub/{pool}", "pools.scrub.start", "Start a scrub", fails(http.StatusBadRequest)), h.startScrub)
	handle(reg, route("DELETE /api/scrub/{pool}", "pools.scrub.cancel", "Cancel a running scrub", fails(http.StatusBadRequest)), h.cancelScrub)
	handle(reg, route("GET /api/scrub-schedules", "pools.scrub.listSchedules", "Periodic scrub schedules"), h.listScrubSchedules)
	handle(reg, route("PUT /api/scrub-schedule/{pool}", "pools.scrub.setSchedule", "Set a pool's scrub schedule", fails(http.StatusBadRequest)), h.setScrubSchedule)
	handle(reg, route("DELETE /api/scrub-schedule/{pool}", "pools.scrub.removeSchedule", "Remove a pool's scrub schedule", fails(http.StatusBadRequest)), h.deleteScrubSchedule)
	handle(reg, route("GET /api/devices", "devices.list", "Block devices, with pool membership"), h.getDevices)

	// Datasets
	handle(reg, route("GET /api/datasets", "datasets.list", "List datasets and volumes"), h.getDatasets)
	handle(reg, route("POST /api/datasets", "datasets.create", "Create a filesystem or volume", fails(http.StatusBadRequest), status(http.StatusCreated)), h.createDataset)
	handle(reg, route("PATCH /api/datasets/{name...}", "datasets.update", "Set dataset properties",
		fails(http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		describe("A mountpoint change is refused with 409 while shares reference the moving paths (#183).")), h.setDatasetProps)
	handle(reg, route("DELETE /api/datasets/{name...}", "datasets.delete", "Destroy a dataset or volume",
		fails(http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		describe("Refused with 409 while shares, iSCSI targets or replication tasks reference it (#183).")), h.deleteDataset)
	handle(reg, route("GET /api/dataset-props/{name...}", "datasets.getProperties", "All properties of a dataset, with source", fails(http.StatusBadRequest)), h.getDatasetProps)
	handle(reg, route("POST /api/datasets/rename", "datasets.rename", "Rename a dataset", fails(http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)), h.renameDataset)
	handle(reg, route("POST /api/rewrite/{name...}", "datasets.rewrite", "Rewrite dataset data in place (background job)",
		fails(http.StatusBadRequest, http.StatusNotFound), status(http.StatusAccepted)), h.rewriteDataset)
	handle(reg, route("GET /api/userspace/{name...}", "datasets.quotas.usage", "Per-user or per-group space usage and quotas", fails(http.StatusBadRequest, http.StatusNotFound)), h.getUserSpace)
	handle(reg, route("POST /api/userquota/{name...}", "datasets.quotas.set", "Set or clear a user or group quota", fails(http.StatusBadRequest, http.StatusNotFound)), h.setUserQuota)
	handle(reg, route("GET /api/chown/{dataset...}", "datasets.owner.get", "Owner and group of the mountpoint", fails(http.StatusBadRequest)), h.getDatasetOwnership)
	handle(reg, route("POST /api/chown/{dataset...}", "datasets.owner.set", "Change owner and group of the mountpoint", fails(http.StatusBadRequest, http.StatusNotFound)), h.setDatasetOwnership)
	handle(reg, route("GET /api/acl-status", "datasets.acl.status", "Which datasets carry ACLs"), h.getACLStatus)
	handle(reg, route("GET /api/acl/{dataset...}", "datasets.acl.get", "ACL of a dataset's mountpoint", fails(http.StatusBadRequest)), h.getDatasetACL)
	handle(reg, route("POST /api/acl/{dataset...}", "datasets.acl.addEntry", "Add or update an ACL entry", fails(http.StatusBadRequest, http.StatusNotFound)), h.setACLEntry)
	handle(reg, route("DELETE /api/acl/{dataset...}", "datasets.acl.removeEntry", "Remove an ACL entry", fails(http.StatusBadRequest, http.StatusNotFound)), h.removeACLEntry)
	handle(reg, route("GET /api/auto-snapshot/{name...}", "datasets.autoSnapshot.get", "Auto-snapshot properties of a dataset", fails(http.StatusBadRequest)), h.getAutoSnapshotProps)
	handle(reg, route("PUT /api/auto-snapshot/{name...}", "datasets.autoSnapshot.set", "Set auto-snapshot properties of a dataset",
		fails(http.StatusBadRequest, http.StatusNotFound)), h.setAutoSnapshotProps)

	// Snapshots
	handle(reg, route("GET /api/snapshots", "snapshots.list", "List snapshots"), h.getSnapshots)
	handle(reg, route("POST /api/snapshots", "snapshots.create", "Create a snapshot", fails(http.StatusBadRequest, http.StatusNotFound), status(http.StatusCreated)), h.createSnapshot)
	handle(reg, route("DELETE /api/snapshots/{snapshot...}", "snapshots.delete", "Destroy a snapshot", fails(http.StatusBadRequest, http.StatusNotFound)), h.deleteSnapshot)
	handle(reg, route("POST /api/snapshots/delete-batch", "snapshots.deleteBatch", "Destroy several snapshots", fails(http.StatusBadRequest)), h.deleteSnapshotBatch)
	handle(reg, route("POST /api/snapshots/clone", "snapshots.clone", "Clone a snapshot into a new dataset", fails(http.StatusBadRequest), status(http.StatusCreated)), h.cloneSnapshot)
	handle(reg, route("POST /api/snapshots/send", "snapshots.send", "Send a snapshot to another dataset (background job)", fails(http.StatusBadRequest, http.StatusNotFound), status(http.StatusAccepted)), h.sendSnapshot)
	handle(reg, route("GET /api/snapshots/diff", "snapshots.diff", "Files changed between a snapshot and a later snapshot or the live filesystem", fails(http.StatusBadRequest, http.StatusNotFound)), h.getSnapshotDiff)

	// Auto-snapshot ownership
	handle(reg, route("GET /api/auto-snapshot-schedules", "autoSnapshot.listSchedules", "Auto-snapshot properties of all datasets"), h.listAutoSnapshotSchedules)
	handle(reg, route("GET /api/auto-snapshot/status", "autoSnapshot.status", "Who runs auto-snapshots (OS daemon or dumpstore) and last runs"), h.getAutoSnapshotStatus)
	handle(reg, route("POST /api/auto-snapshot/takeover", "autoSnapshot.takeover", "Disable the OS daemon and let dumpstore run auto-snapshots"), h.takeoverAutoSnapshot)
	handle(reg, route("POST /api/auto-snapshot/release", "autoSnapshot.release", "Hand auto-snapshots back to the OS daemon"), h.releaseAutoSnapshot)

	// Replication and jobs
	handle(reg, route("GET /api/replication", "replication.list", "List replication tasks"), h.listReplication)
	handle(reg, route("POST /api/replication", "replication.create", "Create a replication task",
		status(http.StatusCreated), fails(http.StatusBadRequest)), h.createReplication)
	handle(reg, route("PATCH /api/replication/{id}", "replication.update", "Update a replication task",
		fails(http.StatusBadRequest, http.StatusNotFound)), h.updateReplication)
	handle(reg, route("DELETE /api/replication/{id}", "replication.delete", "Delete a replication task", status(http.StatusNoContent), fails(http.StatusBadRequest, http.StatusNotFound)), h.deleteReplication)
	handle(reg, route("POST /api/replication/{id}/run", "replication.run", "Run a replication task now (background job)", status(http.StatusAccepted), fails(http.StatusBadRequest, http.StatusNotFound)), h.runReplication)
	handle(reg, route("GET /api/replication/{id}/history", "replication.history", "Recent runs of a replication task", fails(http.StatusBadRequest, http.StatusNotFound)), h.getReplicationHistory)
	handle(reg, route("GET /api/jobs", "jobs.list", "List background jobs"), h.listJobs)
	handle(reg, route("GET /api/jobs/{id}", "jobs.get", "Get a background job", fails(http.StatusNotFound)), h.getJob)
	handle(reg, route("POST /api/jobs/{id}/cancel", "jobs.cancel", "Cancel a running job", status(http.StatusNoContent), fails(http.StatusBadRequest),
		describe("400 also covers an unknown or already finished job.")), h.cancelJob)
	handle(reg, route("DELETE /api/jobs/{id}", "jobs.delete", "Remove a finished job", status(http.StatusNoContent), fails(http.StatusBadRequest),
		describe("400 also covers an unknown or still running job.")), h.removeJob)

	// Users and groups
	handle(reg, route("GET /api/users", "users.list", "List local users"), h.getUsers)
	handle(reg, route("POST /api/users", "users.create", "Create a local user", status(http.StatusCreated), fails(http.StatusBadRequest)), h.createUser)
	handle(reg, route("PUT /api/users/{name}", "users.update", "Modify a local user", fails(http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)), h.modifyUser)
	handle(reg, route("DELETE /api/users/{name}", "users.delete", "Delete a local user", fails(http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)), h.deleteUser)
	handle(reg, route("GET /api/users/{name}/sshkeys", "users.sshKeys.list", "List a user's SSH keys", fails(http.StatusBadRequest, http.StatusNotFound)), h.listSSHKeys)
	handle(reg, route("POST /api/users/{name}/sshkeys", "users.sshKeys.add", "Add an SSH key", fails(http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)), h.addSSHKey)
	handle(reg, route("DELETE /api/users/{name}/sshkeys", "users.sshKeys.remove", "Remove an SSH key (key in the body)", fails(http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)), h.removeSSHKey)
	handle(reg, route("GET /api/groups", "groups.list", "List local groups"), h.getGroups)
	handle(reg, route("POST /api/groups", "groups.create", "Create a local group", status(http.StatusCreated), fails(http.StatusBadRequest)), h.createGroup)
	handle(reg, route("PUT /api/groups/{name}", "groups.update", "Rename a group or change its GID or members", fails(http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)), h.modifyGroup)
	handle(reg, route("DELETE /api/groups/{name}", "groups.delete", "Delete a local group", fails(http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)), h.deleteGroup)

	// SMB
	handle(reg, route("GET /api/smb/status", "smb.status", "Samba initialisation state and wsdd status"), h.getSMBStatus)
	handle(reg, route("POST /api/smb/init", "smb.init", "Initialise Samba (dumpstore takes over smb.conf)", fails(http.StatusUnprocessableEntity)), h.initSamba)
	handle(reg, route("POST /api/smb/wsdd", "smb.wsdd.apply", "Take over the wsdd configuration", fails(http.StatusConflict)), h.applyWSDD)
	handle(reg, route("GET /api/smb-shares", "smb.shares.list", "List SMB usershares", fails(http.StatusConflict)), h.getSMBShares)
	handle(reg, route("POST /api/smb-share/{dataset...}", "smb.shares.set", "Share a dataset over SMB", fails(http.StatusBadRequest, http.StatusConflict)), h.setSMBShare)
	handle(reg, route("DELETE /api/smb-share/{dataset...}", "smb.shares.delete", "Remove an SMB share", fails(http.StatusBadRequest, http.StatusConflict)), h.deleteSMBShare)
	handle(reg, route("GET /api/smb-users", "smb.users.list", "List Samba users"), h.getSambaUsers)
	handle(reg, route("POST /api/smb-users/{name}", "smb.users.add", "Register a user with Samba", fails(http.StatusBadRequest, http.StatusConflict)), h.addSambaUser)
	handle(reg, route("DELETE /api/smb-users/{name}", "smb.users.remove", "Remove a user from Samba", fails(http.StatusBadRequest, http.StatusConflict)), h.removeSambaUser)
	handle(reg, route("GET /api/smb/homes", "smb.homes.get", "The [homes] share"), h.getSMBHomes)
	handle(reg, route("POST /api/smb/homes", "smb.homes.set", "Enable or update the [homes] share", fails(http.StatusBadRequest, http.StatusConflict)), h.setSMBHomes)
	handle(reg, route("DELETE /api/smb/homes", "smb.homes.disable", "Disable the [homes] share", fails(http.StatusConflict)), h.deleteSMBHomes)
	handle(reg, route("GET /api/smb/timemachine", "smb.timeMachine.list", "List Time Machine shares"), h.getTimeMachineShares)
	handle(reg, route("POST /api/smb/timemachine", "smb.timeMachine.set", "Create a Time Machine share", fails(http.StatusBadRequest, http.StatusConflict)), h.createTimeMachineShare)
	handle(reg, route("DELETE /api/smb/timemachine/{sharename}", "smb.timeMachine.delete", "Delete a Time Machine share", fails(http.StatusBadRequest, http.StatusConflict)), h.deleteTimeMachineShare)

	// iSCSI
	handle(reg, route("GET /api/iscsi-targets", "iscsi.targets.list", "List iSCSI targets"), h.getISCSITargets)
	handle(reg, route("POST /api/iscsi-targets", "iscsi.targets.create", "Export a zvol as an iSCSI target",
		fails(http.StatusBadRequest, http.StatusConflict),
		describe("On FreeBSD, refused with 409 until POST /api/iscsi/init has run, or when the IQN or zvol is already exported.")), h.createISCSITarget)
	handle(reg, route("DELETE /api/iscsi-targets", "iscsi.targets.delete", "Delete an iSCSI target",
		fails(http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		describe("On FreeBSD, refused with 409 until POST /api/iscsi/init has run; 404 when no target matches iqn and zvol.")), h.deleteISCSITarget)
	handle(reg, route("GET /api/iscsi/status", "iscsi.status", "iSCSI backend and initialisation state (ctld init gate on FreeBSD)"), h.getISCSIStatus)
	handle(reg, route("POST /api/iscsi/init", "iscsi.init", "Initialise ctld: back up and take over /etc/ctl.conf (FreeBSD)",
		fails(http.StatusBadRequest)), h.initISCSI)

	// Services, TLS, auth
	handle(reg, route("GET /api/services", "services.list", "Status of managed services"), h.getServices)
	handle(reg, route("POST /api/services/{service}/{action}", "services.control", "Start, stop, restart, enable or disable a service",
		fails(http.StatusBadRequest, http.StatusNotFound, http.StatusConflict), describe("409 when the service is not installed on this host.")), h.mutateService)
	handle(reg, route("GET /api/tls/status", "tls.get", "TLS configuration and certificate details"), h.getTLSStatus)
	handle(reg, route("PATCH /api/tls/config", "tls.update", "Use an existing certificate and key", fails(http.StatusBadRequest)), h.tlsSetConfig)
	handle(reg, route("POST /api/tls/gencert", "tls.generateSelfSigned", "Generate a self-signed certificate", fails(http.StatusBadRequest)), h.tlsGenCert)
	handle(reg, route("POST /api/tls/acme/issue", "tls.acmeIssue", "Issue a certificate via ACME", fails(http.StatusBadRequest)), h.tlsAcmeIssue)
	handle(reg, route("POST /api/tls/acme/renew", "tls.acmeRenew", "Renew the ACME certificate", fails(http.StatusBadRequest)), h.tlsAcmeRenew)
	handle(reg, route("POST /api/auth/change-password", "auth.setPassword", "Change the admin password",
		fails(http.StatusBadRequest, http.StatusUnauthorized), describe("401 when the current password is wrong.")), h.changePassword)
	handle(reg, route("POST /api/auth/change-username", "auth.setUsername", "Change the admin username (ends all sessions)", fails(http.StatusBadRequest)), h.changeUsername)

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
